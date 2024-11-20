#!/bin/bash
set -o errexit

namespace="envoy-xds-controller"

# 0. Check commands
function check_command() {
    local command=$1

    if ! command -v $command &> /dev/null; then
        echo "Error: ${command} not found"
        exit 1
    fi
}

check_command kubectl
check_command ip

# 1. Get Net Interface for found local IP
while getopts e: flag
do
    case "${flag}" in
        e) eth=${OPTARG};;
    esac
done

# Set default net interface
if [ -z "$eth" ]
then
    eth="en0"
fi

# 2. Scale running Envoy xDS Controller to 0 replicas
kubectl scale deployment -n ${namespace} exc-envoy-xds-controller --replicas 0

# 3. Create dir for local certificates
mkdir -p /tmp/k8s-webhook-server/serving-certs

# 4. Get generated certificates from Kubernetes
kubectl get secrets -n ${namespace} $(kubectl get secrets -n ${namespace} --no-headers -o custom-columns=":metadata.name" | grep tls) -o jsonpath='{.data.tls\.crt}' | base64 -D > /tmp/k8s-webhook-server/serving-certs/tls.crt
kubectl get secrets -n ${namespace} $(kubectl get secrets -n ${namespace} --no-headers -o custom-columns=":metadata.name" | grep tls) -o jsonpath='{.data.tls\.key}' | base64 -D > /tmp/k8s-webhook-server/serving-certs/tls.key

# 5. Clear services
kubectl delete service -n ${namespace} envoy-xds-controller-webhook-service
kubectl delete service -n ${namespace} exc-envoy-xds-controller-cache-api
kubectl delete service -n ${namespace} exc-envoy-xds-controller

# 6. Get local IP
ip=$(ip a | grep ${eth} -A3 | grep "inet " | awk '{print $2}' | cut -d "/" -f 1)
echo "Local IP: ${ip}"

# 7. Create service for route Webhooks to local Envoy xDS Controller
cat <<EOF | kubectl apply -n ${namespace} -f -
apiVersion: v1
kind: Service
metadata:
  name: envoy-xds-controller-webhook-service
  namespace: ${namespace}
spec:
  ports:
  - port: 443
    protocol: TCP
    targetPort: 9443
---
apiVersion: v1
kind: Endpoints
metadata:
  name: envoy-xds-controller-webhook-service
  namespace: ${namespace}
subsets:
  - addresses:
      - ip: ${ip}
    ports:
      - port: 9443
EOF

# 8. Create service for route Cache API to local Envoy xDS Controller
cat <<EOF | kubectl apply -n ${namespace} -f -
apiVersion: v1
kind: Service
metadata:
  name: exc-envoy-xds-controller-cache-api
  namespace: ${namespace}
spec:
  ports:
  - name: http
    port: 9999
    protocol: TCP
    targetPort: 9999
---
apiVersion: v1
kind: Endpoints
metadata:
  name: exc-envoy-xds-controller-cache-api
  namespace: ${namespace}
subsets:
  - addresses:
      - ip: ${ip}
    ports:
      - name: http
        port: 9999
        protocol: TCP
EOF

# 9. xDS Controller

cat <<EOF | kubectl apply -n ${namespace} -f -
apiVersion: v1
kind: Service
metadata:
  name: exc-envoy-xds-controller
  namespace: ${namespace}
spec:
  ports:
  - name: grpc
    port: 9000
    protocol: TCP
    targetPort: 9000
---
apiVersion: v1
kind: Endpoints
metadata:
  name: exc-envoy-xds-controller
  namespace: ${namespace}
subsets:
  - addresses:
      - ip: ${ip}
    ports:
      - name: grpc
        port: 9000
        protocol: TCP
EOF


# 10. Restart ui
echo "Sleep 10s ..."
sleep 10
kubectl scale deployment -n ${namespace} exc-envoy-xds-controller-ui --replicas 0
kubectl scale deployment -n ${namespace} exc-envoy-xds-controller-ui --replicas 1