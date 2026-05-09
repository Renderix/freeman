#!/bin/bash
set -e
JAVA_HOME=$(/usr/libexec/java_home)
clang -shared -fPIC \
  -I"$JAVA_HOME/include" \
  -I"$JAVA_HOME/include/darwin" \
  $(pkg-config --cflags portaudio-2.0) \
  libportaudio_jni.c \
  $(pkg-config --libs portaudio-2.0) \
  -o libportaudio_jni.dylib
echo "Built libportaudio_jni.dylib"
