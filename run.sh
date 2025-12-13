#!/bin/bash

if [ -z "${ROOT_PATH_DPF}" ]; then
  echo "exiting... must provide ROOT_PATH_DPF environment variable"
  exit 1
fi

# clear out possibly any left over artifacts from imgp in prior runs
# in a directory
clear_file_pattern() {
  path=$1
  pattern=$2
  if find ${path} -maxdepth 1 -name ${pattern} | read; then
    rm ${path}/${pattern}
  fi
}

clear_file_pattern ${ROOT_PATH_DPF}/original "*_IMGP\.*"
clear_file_pattern ${ROOT_PATH_DPF}/original/surprise "*_IMGP\.*"

# rotate all by 90
imgp -o 90 ${ROOT_PATH_DPF}/original/*

# move outputs into imv path for viewing
mv ${ROOT_PATH_DPF}/original/*_IMGP\.* ${ROOT_PATH_DPF}/photos/
mv ${ROOT_PATH_DPF}/original/surprise/*_IMGP\.* ${ROOT_PATH_DPF}/photos/surprise/

# start imv
app=$(pgrep imv-wayland)
if [ -n "${app}" ]; then
  pkill imv-wayland
fi
/usr/bin/imv-wayland -f -s full -t 15 -r ${ROOT_PATH_DPF}/photos
