# https://docs.opentripplanner.org/en/v2.2.0/Container-Image/
docker run --rm -v ./build:/var/opentripplanner docker.io/opentripplanner/opentripplanner:latest --build --save