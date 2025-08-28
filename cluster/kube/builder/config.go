package builder

const (
	// Config system constants
	AkashConfigVolume   = "akash-cfg"
	AkashConfigMount    = "/akash-cfg"
	AkashConfigInitName = "akash-init"
	AkashConfigEnvFile  = "config.env"

	// RBAC constants
	AkashRoleName    = "akash-role"
	AkashRoleBinding = "akash-binding"

	// Init container script
	akashInitScript = `
       # Install jq
       apk add --no-cache jq curl &>/dev/null

       # Define default paths if not set
       AKASH_CONFIG_PATH="${AKASH_CONFIG_PATH:-/akash/config}"
       AKASH_CONFIG_FILE="${AKASH_CONFIG_FILE:-env.sh}"
       
       # Validate paths
       [ "$AKASH_CONFIG_PATH" = "/" ] && AKASH_CONFIG_PATH="/tmp/akash"
       AKASH_CONFIG_PATH="${AKASH_CONFIG_PATH%/}"

       # Create config directory if it doesn't exist
       mkdir -p "${AKASH_CONFIG_PATH}"

       if [ "$AKASH_REQUIRES_NODEPORT" != "true" ]; then
           touch "${AKASH_CONFIG_PATH}/${AKASH_CONFIG_FILE}"
           echo "# No NodePorts required" >> "${AKASH_CONFIG_PATH}/${AKASH_CONFIG_FILE}"
           exit 0
       fi

       # Get service information using the Kubernetes API
       NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)
       TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
       CACERT=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt
       API_SERVER="https://kubernetes.default.svc:443"
       BASE_API_URL="${API_SERVER}/api/v1/namespaces/${NAMESPACE}"
       
       # Function to get first valid service using jq
       get_valid_service() {
           local service_name="$1"
           local services_json
           
           services_json=$(curl -s --cacert "${CACERT}" -H "Authorization: Bearer ${TOKEN}" "${BASE_API_URL}/services/")
           
           echo "$services_json" | jq -r "
               (.items[] | select(.metadata.name == \"${service_name}\") | .metadata.name) // 
               (.items[] | select(.metadata.name == \"${service_name}-np\") | .metadata.name) //
               (.items[] | select(.metadata.name | contains(\"${service_name}\")) | .metadata.name) // 
               empty
           " | head -n 1
       }
       
       # Get the valid service name
       ACTUAL_SERVICE_NAME=$(get_valid_service "${SERVICE_NAME}")
       
       [ -z "$ACTUAL_SERVICE_NAME" ] && ACTUAL_SERVICE_NAME="${SERVICE_NAME}"
       
       API_URL="${BASE_API_URL}/services/${ACTUAL_SERVICE_NAME}"
       TEMP_FILE="${AKASH_CONFIG_PATH}/.tmp.${AKASH_CONFIG_FILE}"
       CONFIG_FILE="${AKASH_CONFIG_PATH}/${AKASH_CONFIG_FILE}"
       
       # Create initial config file header
       echo "# Akash config generated on $(date)" > "$TEMP_FILE"
       echo "# Service: ${ACTUAL_SERVICE_NAME}" >> "$TEMP_FILE"
       
       # Add retries with exponential backoff
       MAX_ATTEMPTS=30
       for i in $(seq 1 $MAX_ATTEMPTS); do
           # Query the service to get NodePort mappings
           RESPONSE=$(curl -s --max-time 5 --retry 3 --retry-delay 1 --cacert "${CACERT}" \
             -H "Authorization: Bearer ${TOKEN}" \
             "${API_URL}")
           
           # Check service type first
           SERVICE_TYPE=$(echo "$RESPONSE" | jq -r '.spec.type // "unknown"')
           
           if [ "$SERVICE_TYPE" = "NodePort" ]; then
               # Service is NodePort, extract nodePort values
               NODE_PORTS=$(echo "$RESPONSE" | jq -r '.spec.ports[] | select(.nodePort != null) | "export AKASH_EXTERNAL_PORT_\(.targetPort)+=\(.nodePort)"' 2>/dev/null || echo "")
               
               if [ -n "$NODE_PORTS" ]; then
                   echo "$NODE_PORTS" >> "$TEMP_FILE"
                   mv "$TEMP_FILE" "$CONFIG_FILE"
                   exit 0
               fi
           elif [ "$SERVICE_TYPE" = "LoadBalancer" ]; then
               # Service is LoadBalancer, check for external IPs
               EXTERNAL_IPS=$(echo "$RESPONSE" | jq -r '.status.loadBalancer.ingress[]?.ip // empty' 2>/dev/null || echo "")
               if [ -n "$EXTERNAL_IPS" ]; then
                   echo "export AKASH_EXTERNAL_IP+=${EXTERNAL_IPS}" >> "$TEMP_FILE"
                   mv "$TEMP_FILE" "$CONFIG_FILE"
                   exit 0
               fi
           elif [ "$SERVICE_TYPE" = "ClusterIP" ]; then
               # Service is ClusterIP with dedicated IP
               echo "# Service type is ClusterIP with dedicated IP" >> "$TEMP_FILE"
               
               # Get service ports for reference
               PORTS=$(echo "$RESPONSE" | jq -r '.spec.ports[] | "# Port \(.port) -> \(.targetPort)"' 2>/dev/null || echo "# No port mappings found")
               echo "$PORTS" >> "$TEMP_FILE"
               
               # Move to final file after waiting for some time, in case it's still being configured
               if [ $i -gt 5 ]; then
                   mv "$TEMP_FILE" "$CONFIG_FILE"
                   exit 0
               fi
           fi
           
           # Exponential backoff with max of 10 seconds
           SLEEP_TIME=$((2 ** ((i-1) > 3 ? 3 : (i-1))))
           sleep $SLEEP_TIME
       done

       # Create empty config file to prevent container from failing
       echo "# Warning: Service configuration timeout after $MAX_ATTEMPTS attempts" >> "$TEMP_FILE"
       echo "# Service type: ${SERVICE_TYPE}" >> "$TEMP_FILE"
       mv "$TEMP_FILE" "$CONFIG_FILE"
       exit 0
`
)
