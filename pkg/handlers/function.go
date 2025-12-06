package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/2025-softbank-hackathon-final-navy/execution/config"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/k8s"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/redis"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/types"
	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"context"
)

func HandleDeployAndRun(c *gin.Context) {
	bodyBytes, _ := io.ReadAll(c.Request.Body)
	var req types.FunctionRequest
	json.Unmarshal(bodyBytes, &req)

	var funcID string
	if req.Name != "" {
		funcID = req.Name
	} else {
		hash := sha256.Sum256([]byte(req.Code))
		funcID = "func-" + hex.EncodeToString(hash[:])[:10]
	}

	redis.SaveFunctionToRedis(req, funcID)
	redis.UpdateLastActivity(funcID)
	ensureK8sResources(c, funcID, req, bodyBytes)
}

func HandleInvoke(c *gin.Context) {
	funcID := c.Param("name")
	bodyBytes, _ := io.ReadAll(c.Request.Body)

	redis.UpdateLastActivity(funcID)

	_, err := k8s.Clientset.CoreV1().Services(config.NAMESPACE).Get(context.TODO(), funcID, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		meta, err := redis.GetFunctionFromRedis(funcID)
		if err != nil {
			c.JSON(404, gin.H{"error": "Not found"})
			return
		}
		req := types.FunctionRequest{Name: meta.Name, Code: meta.Code, Type: meta.Type, Request: meta.Request}
		ensureK8sResources(c, funcID, req, bodyBytes)
		return
	}
	target := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", funcID, config.NAMESPACE)
	proxyRequest(c, funcID, target, bodyBytes)
}

func ensureK8sResources(c *gin.Context, funcID string, req types.FunctionRequest, bodyBytes []byte) {
	_, err := k8s.Clientset.CoreV1().Services(config.NAMESPACE).Get(context.TODO(), funcID, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		if err := k8s.CreateK8sResources(funcID, req, false); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		if !k8s.WaitForPodReady(funcID) {
			c.JSON(504, gin.H{"error": "Timeout"})
			return
		}
	}
	target := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", funcID, config.NAMESPACE)
	proxyRequest(c, funcID, target, bodyBytes)
}

func proxyRequest(c *gin.Context, funcID string, target string, bodyBytes []byte) {
	redis.IncRequestAndScale(funcID)
	defer redis.DecRequest(funcID)

	remote, _ := url.Parse(target)
	proxy := httputil.NewSingleHostReverseProxy(remote)
	proxy.Transport = &http.Transport{DisableKeepAlives: false, MaxIdleConnsPerHost: 10}

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = remote.Host
		req.URL.Scheme = remote.Scheme
		req.URL.Host = remote.Host
		req.URL.Path = "/"
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
		req.Header.Set("Content-Length", fmt.Sprintf("%d", len(bodyBytes)))
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(fmt.Sprintf("Gateway Error: %v", err)))
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}
