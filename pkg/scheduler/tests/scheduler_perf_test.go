/*
Copyright 2019 Cloudera, Inc.  All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
    "fmt"
    "github.com/cloudera/yunikorn-core/pkg/common/configs"
    "github.com/cloudera/yunikorn-core/pkg/entrypoint"
    "github.com/cloudera/yunikorn-core/pkg/log"
    "github.com/cloudera/yunikorn-scheduler-interface/lib/go/si"
    "go.uber.org/zap"
    "strconv"
    "testing"
    "time"
)

func benchmarkScheduling(b *testing.B, numNodes, numPods int) {
    log.InitAndSetLevel(zap.InfoLevel)
    // Start all tests
    serviceContext := entrypoint.StartAllServices()
    defer serviceContext.StopAll()
    proxy := serviceContext.RMProxy

    // Register RM
    configData := `
partitions:
  -
    name: default
    queues:
      - name: root
        submitacl: "*"
        queues:
          - name: a
            resources:
              guaranteed:
                memory: 100000
                vcore: 10000
          - name: b
            resources:
              guaranteed:
                memory: 1000000
                vcore: 10000
`
    configs.MockSchedulerConfigByData([]byte(configData))
    mockRM := NewMockRMCallbackHandler(b)

    _, err := proxy.RegisterResourceManager(
        &si.RegisterResourceManagerRequest{
            RmId:        "rm:123",
            PolicyGroup: "policygroup",
            Version:     "0.0.2",
        }, mockRM)

    if err != nil {
        b.Error(err.Error())
    }

    // Add two apps and wait for them to be accepted
    err = proxy.Update(&si.UpdateRequest{
        NewApplications: newAddAppRequest(map[string]string{"app-1":"root.a", "app-2": "root.b"}),
        RmId: "rm:123",
    })
    if nil != err {
        b.Error(err.Error())
    }
    waitForAcceptedApplications(mockRM, "app-1", 1000)
    waitForAcceptedApplications(mockRM, "app-2", 1000)

    // Calculate node resources to make sure all required pods can be allocated
    requestMem := 10
    requestVcore := 1
    numPodsPerNode := numPods / numNodes + 1
    nodeMem := requestMem * numPodsPerNode
    nodeVcore := requestVcore * numPodsPerNode

    // Register nodes
    var newNodes []*si.NewNodeInfo
    for i:=0; i < numNodes; i++ {
        nodeName := "node-" + strconv.Itoa(i)
        node := &si.NewNodeInfo{
            NodeId:
            nodeName + ":1234",
            Attributes: map[string]string{
                "si.io/hostname": nodeName,
                "si.io/rackname": "rack-1",
            },
            SchedulableResource: &si.Resource{
                Resources: map[string]*si.Quantity{
                    "memory": {Value: int64(nodeMem)},
                    "vcore":  {Value: int64(nodeVcore)},
                },
            },
        }
        newNodes = append(newNodes, node)
    }
    err = proxy.Update(&si.UpdateRequest{
        RmId: "rm:123",
        NewSchedulableNodes: newNodes,
    })

    // Wait for all nodes to be accepted
    startTime := time.Now()
    waitForMinNumberOfAcceptedNodes(mockRM, numNodes, 5000)
    duration := time.Now().Sub(startTime)
    b.Logf("Total time to add %d node in %s, %f per second", numNodes, duration, float64(numNodes) / duration.Seconds())

    // Request pods
    app1NumPods := numPods / 2
    err = proxy.Update(&si.UpdateRequest{
        Asks: []*si.AllocationAsk{
            {
                AllocationKey: "alloc-1",
                ResourceAsk: &si.Resource{
                    Resources: map[string]*si.Quantity{
                        "memory": {Value: int64(requestMem)},
                        "vcore":  {Value: int64(requestVcore)},
                    },
                },
                MaxAllocations: int32(app1NumPods),
                ApplicationId:  "app-1",
            },
        },
        RmId: "rm:123",
    })
    err = proxy.Update(&si.UpdateRequest{
        Asks: []*si.AllocationAsk{
            {
                AllocationKey: "alloc-1",
                ResourceAsk: &si.Resource{
                    Resources: map[string]*si.Quantity{
                        "memory": {Value: int64(requestMem)},
                        "vcore":  {Value: int64(requestVcore)},
                    },
                },
                MaxAllocations: int32(numPods-app1NumPods),
                ApplicationId:  "app-2",
            },
        },
        RmId: "rm:123",
    })

    // Reset number of iterations and timer for this benchmark
    b.N = numPods
    startTime = time.Now()
    b.ResetTimer()

    // Wait for all pods to be allocated
    waitForMinAllocations(mockRM, numPods, 120000)

    // Stop timer and calculate duration
    b.StopTimer()
    duration = time.Now().Sub(startTime)

    b.Logf("Total time to allocate %d containers in %s, %f per second", numPods, duration, float64(numPods) / duration.Seconds())
}

func BenchmarkScheduling(b *testing.B) {
    tests := []struct{ numNodes, numPods int }{
        {numNodes: 500, numPods: 10000},
        {numNodes: 1000, numPods: 10000},
        {numNodes: 2000, numPods: 10000},
        {numNodes: 5000, numPods: 10000},
    }
    for _, test := range tests {
        name := fmt.Sprintf("%vNodes/%vPods", test.numNodes, test.numPods)
        b.Run(name, func(b *testing.B) {
            benchmarkScheduling(b, test.numNodes, test.numPods)
        })
    }
}