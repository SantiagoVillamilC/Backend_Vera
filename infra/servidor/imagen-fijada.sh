#!/usr/bin/env bash
# infra/servidor/imagen-fijada.sh
# SO e imagen fijados para los tres destinos. Ver infra/servidor/README.md.

# Nunca ":latest": rompería la premisa de que los tres destinos arrancan
# del mismo sistema.
: "${IMAGEN_URN:=Debian:debian-13:13-gen2:0.20260831.2587}"

: "${IMAGEN_VERSION_ESPERADA:=0.20260831.2587}"
: "${SERIAL_IMAGEN_DEBIAN:=20260831-2587}"
: "${IMAGEN_OS_ID:=debian}"
: "${IMAGEN_OS_VERSION_ID:=13}"
