
1. 开发运行 
CGO_ENABLED=0 go run  sigs.k8s.io/descheduler/cmd/descheduler --policy-config-file examples/develop.yaml --kubeconfig /tmp/admin.conf
CGO_ENABLED=0 go run  sigs.k8s.io/descheduler/cmd/descheduler --policy-config-file examples/develop.yaml --kubeconfig /tmp/admin.conf -v 3

2. 添加新的依赖包
    1. 修改go.mod
    2. go mod vendor