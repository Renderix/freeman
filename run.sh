#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
exec java -Djava.library.path="$DIR" \
          -jar "$DIR/freeman.jar" \
          "${1:-$DIR/config.yaml}"
