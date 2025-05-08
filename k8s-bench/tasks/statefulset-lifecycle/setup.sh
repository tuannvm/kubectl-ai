#!/bin/bash
# Teardown any existing namespace
kubectl delete namespace statefulset-test --ignore-not-found

# Create namespace
kubectl create namespace statefulset-test

# Create headless Service for StatefulSet
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: db
  namespace: statefulset-test
spec:
  clusterIP: None
  selector:
    app: db
EOF

# Deploy StatefulSet with 3 replicas and 1Gi persistent volume
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: db
  namespace: statefulset-test
spec:
  serviceName: "db"
  replicas: 3
  selector:
    matchLabels:
      app: db
  template:
    metadata:
      labels:
        app: db
    spec:
      containers:
      - name: db
        image: busybox
        command: ["sh","-c","echo test > /data/test && sleep 3600"]
        volumeMounts:
        - name: data
          mountPath: /data
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 1Gi
EOF

# Wait until all 3 replicas are ready
for i in {1..60}; do
  ready=$(kubectl get statefulset db -n statefulset-test -o jsonpath='{.status.readyReplicas}')
  if [[ "$ready" == "3" ]]; then
    exit 0
  fi
  sleep 2
done

echo "StatefulSet did not become ready in time"
exit 1
