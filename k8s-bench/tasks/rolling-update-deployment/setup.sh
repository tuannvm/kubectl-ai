#!/bin/bash
# Initialize namespace and deployment with the old image
kubectl delete namespace rollout-test --ignore-not-found
kubectl create namespace rollout-test
kubectl create deployment web-app --image=nginx:1.21 --replicas=3 -n rollout-test

# Wait until all replicas are available
for i in {1..60}; do
  ready=$(kubectl get deployment web-app -n rollout-test -o jsonpath='{.status.availableReplicas}')
  if [[ "$ready" == "3" ]]; then
    exit 0
  fi
  sleep 1
done

echo "Initial deployment did not become ready in time"
exit 1
