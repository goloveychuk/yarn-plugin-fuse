#!/bin/bash

FUSE_PLUGIN_PATH=${FUSE_PLUGIN_PATH:-../bundles/@yarnpkg/plugin-fuse.js}

cd example && yarn plugin import $FUSE_PLUGIN_PATH && yarn install && yarn test

