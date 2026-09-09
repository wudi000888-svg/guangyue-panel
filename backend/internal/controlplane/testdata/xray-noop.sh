#!/bin/sh
case "$2" in
    inbounduser) printf '{"users":[]}\n' ;;
    adu) printf 'Added 0 user(s) in total.\n' ;;
    adrules) printf 'result: ok\n' ;;
    *) exit 1 ;;
esac
