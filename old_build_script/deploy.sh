#!/bin/bash

RED="\e[31m"
GREEN="\e[32m"
BLUE="\e[34m"
YELLOW="\e[33m"
NC="\e[0m"

logSuccess() { echo -e "$GREEN-----$message-----$NC";}
logError() { echo -e "$RED-----$message-----$NC";}
logInfo() { echo -e "$YELLOW###############---$message---###############$NC";}

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
