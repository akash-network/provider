#!/usr/bin/env bash

akash_api=$(go list -mod=readonly -m -f '{{ .Dir }}' github.com/akash-network/akash-api)

if [ ! -d "${akash_api}/vendor" ]; then
    echo "for script to work akash-api must be checkout locally and replace in go.work"
    exit 1
fi

short_opts=h
long_opts=help/host:/mode: # those who take an arg END with :

host=localhost:8081
mode=

while getopts ":$short_opts-:" o; do
    case $o in
        :)
            echo >&2 "option -$OPTARG needs an argument"
            continue
            ;;
        '?')
            echo >&2 "bad option -$OPTARG"
            continue
            ;;
        -)
            o=${OPTARG%%=*}
            OPTARG=${OPTARG#"$o"}
            lo=/$long_opts/
            case $lo in
                *"/$o"[!/:]*"/$o"[!/:]*)
                    echo >&2 "ambiguous option --$o"
                    continue
                    ;;
                *"/$o"[:/]*)
                    ;;
                *)
                    o=$o${lo#*"/$o"};
                    o=${o%%[/:]*}
                    ;;
            esac

            case $lo in
                *"/$o/"*)
                    OPTARG=
                    ;;
                *"/$o:/"*)
                    case $OPTARG in
                        '='*)
                            OPTARG=${OPTARG#=}
                            ;;
                        *)
                            eval "OPTARG=\$$OPTIND"
                            if [ "$OPTIND" -le "$#" ] && [ "$OPTARG" != -- ]; then
                                OPTIND=$((OPTIND + 1))
                            else
                                echo >&2 "option --$o needs an argument"
                                continue
                            fi
                            ;;
                    esac
                    ;;
            *) echo >&2 "unknown option --$o"; continue;;
            esac
    esac
    case "$o" in
        host)
            host=$OPTARG
            ;;
        mode)
            case "$OPTARG" in
            plaintext|insecure)
                ;;
            *)
                echo >&2 "option --$o can be plaintext|insecure"
                ;;
            esac

            mode=-$OPTARG
            ;;
    esac
done
shift "$((OPTIND - 1))"

grpcurl \
	"$mode" \
	-use-reflection \
	-proto="${akash_api}/proto/provider/akash/inventory/v1/service.proto" \
	-proto="${akash_api}/proto/provider/akash/provider/v1/service.proto" \
	-import-path="${akash_api}/proto/provider" \
	-import-path="${akash_api}/proto/node" \
	-import-path="${akash_api}/vendor/github.com/cosmos/cosmos-sdk/third_party/proto" \
	-import-path="${akash_api}/vendor" \
	"$host" \
	"$@"
