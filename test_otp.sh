# https://docs.opentripplanner.org/en/v2.2.0/Container-Image/
docker run -it --rm -p 8080:8080 -v ./build:/var/opentripplanner docker.io/opentripplanner/opentripplanner:2.8.1 --load --serve