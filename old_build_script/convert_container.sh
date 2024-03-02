#!/bin/bash

RED="\e[31m"
GREEN="\e[32m"
BLUE="\e[34m"
YELLOW="\e[33m"
NC="\e[0m"

logSuccess() { echo -e "$GREEN-----$message-----$NC";}
logError() { echo -e "$RED-----$message-----$NC";}
logInfo() { echo -e "$YELLOW###############---$message---###############$NC";}

message="change image from docker to crictl" && logInfo
image=$(docker images | grep ko.local | grep latest | awk '{print $1}'):latest
docker rmi -f daohiep22/watashino-kourier:latest
docker image tag $image docker.io/daohiep22/watashino-kourier:latest
docker rmi $image
image=$(docker images | grep ko.local | awk '{print $1}'):$(docker images | grep ko.local | awk '{print $2}')
docker rmi $image
docker save -o watashino-kourier.tar docker.io/daohiep22/watashino-kourier:latest
message="Saved atarashi-imeji to .tar file" && logSuccess
sudo crictl rmi docker.io/daohiep22/watashino-kourier:latest
sudo ctr -n=k8s.io images import watashino-kourier.tar
message="Untar atarashi-imeji" && logSuccess
rm -rf watashino-kourier.tar
