DOCKER_BUILDKIT=1 docker build --no-cache -t execution .

docker save -o execution.tar execution:latest

scp execution.tar gyungdal@10.2.0.204:~/
scp execution.tar gyungdal@10.2.0.205:~/

# worker
sudo ctr -n k8s.io images import execution.tar
sudo ctr -n k8s.io images tag docker.io/library/execution:latest localhost/execution:latest
sudo crictl images | grep execution

# kubectl get rs
# kubectl delete rs func-d3f2979475-698d7d5bf8
# kubectl rollout restart deployment execution

# kubectl get svc 해서 나온 IP:PORT로 테스트 스크립트 돌려야함

# kubectl logs -l app=execution-server -f