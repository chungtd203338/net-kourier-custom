#!/bin/bash

RED="\e[31m"
GREEN="\e[32m"
BLUE="\e[34m"
YELLOW="\e[33m"
NC="\e[0m"

logSuccess() { echo -e "$GREEN-----$message-----$NC";}
logError() { echo -e "$RED-----$message-----$NC";}
logInfo() { echo -e "$YELLOW###############---$message---###############$NC";}

message="ko build image" && logInfo
export KO_DOCKER_REPO="ko.local"
ko build cmd/kourier/main.go
if [ "$?" -ne "0" ]; then
    message="ko build error" && logError
    exit 1
else
    message="ko build successfully" && logSuccess
fi
