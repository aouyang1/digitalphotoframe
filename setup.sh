#!/bin/bash
sudo apt update && sudo apt upgrade -y
sudo apt install vim imv imgp golang -y
curl "https://awscli.amazonaws.com/awscli-exe-linux-aarch64.zip" -o "awscliv2.zip"
unzip awscliv2.zip

aws configure
aws s3 ls s3://${DPF_S3_BUCKET} --profile ${DPF_AWS_PROFILE}

mkdir -p ~/digitalphotoframe/original/surprise
