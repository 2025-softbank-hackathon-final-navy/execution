package main

import (
	"log"

	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/handlers"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/k8s"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/monitor"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/redis"
	"github.com/gin-gonic/gin"
)

func main() {
	if err := k8s.InitKubeClient(); err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}

	redis.InitRedisClient()

	go monitor.StartIdleMonitor()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.POST("/run", handlers.HandleDeployAndRun)
	r.POST("/invoke/:name", handlers.HandleInvoke)

	log.Println("Scalable Serverless Gateway started on :80")
	if err := r.Run(":80"); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}