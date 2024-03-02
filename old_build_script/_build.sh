#!/bin/bash

RED="\e[31m"
GREEN="\e[32m"
BLUE="\e[34m"
YELLOW="\e[33m"
NC="\e[0m"

REDBGR='\033[0;41m'
NCBGR='\033[0m'

logSuccess() { echo -e "$GREEN-----$message-----$NC";}
logError() { echo -e "$RED-----$message-----$NC";}
logInfo() { echo -e "$YELLOW###############---$message---###############$NC";}

clear

echo -e "$REDBGR このスクリプトはボナちゃんによって書かれています $NCBGR"

message="ko build image" && logInfo
export KO_DOCKER_REPO="ko.local"
ko build cmd/kourier/main.go
if [ "$?" -ne "0" ]; then
    message="ko build error" && logError
    exit 1
else
    message="ko build successfully" && logSuccess
fi

echo -e "\n"
message="change image from docker to crictl" && logInfo
image=$(docker images | grep ko.local | grep kourier | grep latest | awk '{print $1}'):latest
docker rmi -f daohiep22/watashino-kourier:latest
docker image tag $image docker.io/daohiep22/watashino-kourier:latest
docker rmi $image
image=$(docker images | grep ko.local | grep kourier | awk '{print $1}'):$(docker images | grep ko.local | grep kourier | awk '{print $2}')
docker rmi $image
docker save -o watashino-kourier.tar docker.io/daohiep22/watashino-kourier:latest
message="Saved atarashi-imeji to .tar file" && logSuccess
sudo crictl rmi docker.io/daohiep22/watashino-kourier:latest
sudo ctr -n=k8s.io images import watashino-kourier.tar
message="Untar atarashi-imeji" && logSuccess
rm -rf watashino-kourier.tar

echo -e "\n"
message="remove current Pod" && logInfo
pod=$(kubectl -n knative-serving get pod | grep net-kourier | awk '{print $1}')
kubectl -n knative-serving delete pod/$pod

pod=$(kubectl -n knative-serving get pod | grep net-kourier | awk '{print $1}')
kubectl -n knative-serving wait --for=condition=ready pod $pod > /dev/null 2>&1
sleep 1
curlStatus=$(curl -I hello.default.svc.cluster.local | head -n 1| cut -d $' ' -f2)
echo $curlStatus
clear
if [ $curlStatus -eq "200" ]; then
    message="curl success" && logSuccess
else
    message="curl failed" && logError
fi
message="net-kourier logs" && logInfo
kubectl -n knative-serving logs $pod -f
