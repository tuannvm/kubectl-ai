#!/bin/bash
# Wait until HPA scales above 1 replica
for i in {1..60}; do
  current=$(kubectl get hpa web-app -n hpa-test -o jsonpath='{.status.currentReplicas}')
  if [[ "$current" -gt 1 ]]; then
    exit 0
  fi
  sleep 2
done

echo "HPA did not scale above 1 replica in time"
exit 1
