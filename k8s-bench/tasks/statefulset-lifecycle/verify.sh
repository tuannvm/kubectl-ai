#!/bin/bash
# Scale up to 5 replicas
kubectl scale statefulset db --replicas=5 -n statefulset-test
kubectl wait statefulset/db -n statefulset-test --for=condition=Ready --timeout=120s

# Verify pods db-0 through db-4 exist and data persists
for i in {0..4}; do
  pod="db-$i"
  kubectl get pod "$pod" -n statefulset-test || exit 1
  data=$(kubectl exec "$pod" -n statefulset-test -- cat /data/test)
  if [[ "$data" != "test" ]]; then
    echo "Data missing or incorrect in $pod"
    exit 1
  fi
done

# Scale down to 2 replicas
kubectl scale statefulset db --replicas=2 -n statefulset-test
kubectl wait statefulset/db -n statefulset-test --for=condition=Ready --timeout=120s

# Verify only db-0 and db-1 remain
for pod in db-0 db-1; do
  kubectl get pod "$pod" -n statefulset-test || exit 1
done
for pod in db-2 db-3 db-4; do
  if kubectl get pod "$pod" -n statefulset-test &>/dev/null; then
    echo "Unexpected pod $pod still exists"
    exit 1
  fi
done

exit 0
