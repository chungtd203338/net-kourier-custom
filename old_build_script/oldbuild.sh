#!/bin/bash

OPTION=$1

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

if [ $OPTION == "ko" ]; then
    image=$(docker images | grep ko.local | awk '{print $3}')
    docker rmi -f $image
    ./kobuild.sh
elif [ $OPTION == "dep" ]; then
    ./convert_container.sh
    ./deploy.sh
elif [ $OPTION == "log" ]; then
    ./deploy.sh
elif [ $OPTION == "ful" ]; then
    ./kobuild.sh
    if [ $? -eq "0" ]; then
        ./convert_container.sh
        ./deploy.sh
    else
        exit 1
    fi
elif [ $OPTION == "clean" ]; then
    go clean -cache
    ./hack/update-deps.sh --upgrade
    ./hack/update-codegen.sh
    clear
    ./_build.sh
fi
