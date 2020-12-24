### LowNodeActualUtilization

This strategy finds nodes that are under utilized and evicts pods, if possible, from other nodes
in the hope that recreation of evicted pods will be scheduled on these underutilized nodes. The
parameters of this strategy are configured under `nodeResourceActualUtilizationThresholds`.

The under utilization of nodes is determined by a configurable threshold `thresholds`. The threshold
`thresholds` can be configured for cpu and memory in terms of percentage. If a node's
usage is below threshold for all (cpu, memory), the node is considered underutilized.
"kubectl top pod" are considered for computing node resource utilization.

There is another configurable threshold, `targetThresholds`, that is used to compute those potential nodes
from where pods could be evicted. If a node's usage is above targetThreshold for any (cpu, memory),
the node is considered over utilized. Any node between the thresholds, `thresholds` and `targetThresholds` is
considered appropriately utilized and is not considered for eviction. The threshold, `targetThresholds`,
can be configured for cpu and memory too in terms of percentage.

These thresholds, `thresholds` and `targetThresholds`, could be tuned as per your cluster requirements.

**Parameters:**

|Name|Type|
|---|---|
|`thresholds`|map(string:int)|
|`targetThresholds`|map(string:int)|
|`numberOfNodes`|int|
|`thresholdPriority`|int (see [priority filtering](#priority-filtering))|
|`thresholdPriorityClassName`|string (see [priority filtering](#priority-filtering))|
|`limitNumberOfTargetNodes`|int|
|`excludeOwnerKinds`|list(string)|

**Example:**

```yaml
apiVersion: "descheduler/v1alpha1"
kind: "DeschedulerPolicy"
maxNoOfPodsToEvictPerNode: 1 # default 1
strategies:
  "LowNodeActualUtilization":
      enabled: true
      params:
        nodeResourceActualUtilizationThresholds:
          limitNumberOfTargetNodes: 1 # default 1
          excludeOwnerKinds:
          - "Cronjob"
          - "Job"
          thresholds:
            "cpu" : 30
            "memory": 30
          targetThresholds:
            "cpu" : 60
            "memory": 60
```

Policy should pass the following validation checks:
* Only two types of resources are supported: `cpu`and `memory`.
* `thresholds` or `targetThresholds` can not be nil and they must configure exactly the same types of resources.
* The valid range of the resource's percentage value is \[0, 100\]
* Percentage value of `thresholds` can not be greater than `targetThresholds` for the same resource.

If any of the resource types is not specified, all its thresholds default to 100% to avoid nodes going
from underutilized to overutilized.

* There are other parameters associated with the `LowNodeUtilization` strategy.  
    - Called `numberOfNodes`: This parameter can be configured to activate the strategy only when the number of under utilized nodes
    are above the configured value. This could be helpful in large clusters where a few nodes could go
    under utilized frequently or for a short period of time. By default, `numberOfNodes` is set to zero.
    - Called `limitNumberOfTargetNodes`: This  parameter can be configured to operate how many node per time. This can prevent unexpected accidence happen in production enviroment.
    - Called `excludeOwnerKinds`: This parameter can be configuerd to exclude some Kind of Pod.You can configure this parameter if you don't want to evict a runing `job` for resource over utilization of node.
  

### use in production enviroment
```bash
cd kubernetes
# create rbac and configmap
kubectl create -f base/rbac.yaml
kubectl create -f base/configmap-new.yaml
# create cronjob
kubectl create -f cronjob/cronjob.yaml
```