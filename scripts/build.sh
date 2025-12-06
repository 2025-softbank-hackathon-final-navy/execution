go get github.com/gin-gonic/gin 
go get k8s.io/client-go@v0.28.2 
go get k8s.io/api@v0.28.2 
go get k8s.io/apimachinery@v0.28.2
go get github.com/redis/go-redis/v9 

go mod tidy

CGO_ENABLED=0 GOOS=linux go build -o execution main.go