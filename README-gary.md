
## 开发运行 
CGO_ENABLED=0 go run  sigs.k8s.io/descheduler/cmd/descheduler --policy-config-file examples/develop.yaml --kubeconfig /tmp/admin.conf
CGO_ENABLED=0 go run  sigs.k8s.io/descheduler/cmd/descheduler --policy-config-file examples/develop.yaml --kubeconfig /tmp/admin.conf -v 3

## 添加新的依赖包
    1. 修改go.mod
    2. go mod vendor

## 参考
0. origin
https://github.com/kubernetes-sigs/descheduler
1. KEP
https://docs.google.com/document/d/1ffBpzhqELmhqJxdGMzYzIOoigxn3J0zlP1_nie34f9s/edit#heading=h.defm38wxvjp1
https://github.com/kubernetes-sigs/scheduler-plugins/tree/master/kep/61-Trimaran-real-load-aware-scheduling
2. metrics 
https://stackoverflow.com/questions/52029656/how-to-retrieve-kubernetes-metrics-via-client-go-and-golang
https://github.com/kubernetes/metrics
