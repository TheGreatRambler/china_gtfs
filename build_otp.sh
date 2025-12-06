# More memory allocated than normal
# as OpenTripPlanner uses a lot of memory during the build phase
OTP_HEAP_GB=16
JAVA_MEM="-Xms${OTP_HEAP_GB}g -Xmx${OTP_HEAP_GB}g"

# https://docs.opentripplanner.org/en/v2.2.0/Container-Image/
docker run --rm \
  --memory="${OTP_HEAP_GB}g" --memory-swap="${OTP_HEAP_GB}g" \
  -e "JAVA_TOOL_OPTIONS=${JAVA_MEM}" \
  -v ./build:/var/opentripplanner \
  docker.io/opentripplanner/opentripplanner:latest \
  --build --save