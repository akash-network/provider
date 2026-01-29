# Debugging provider code in kube cluster


Debug mode uses docker images with -debug suffix.
Set env `AKASH_DEBUG=true` to start provider service in debug mode.
**note**, the service itself does not start until debug session is attached.

Default debug port is 2345. Port can be changed via
`AKASH_DEBUG_DELVE_PORT`.

to attach to debugger proxy port to localhost using kubectl.
```shell
kubectl -n akash-services port-forward pods/akash-provider-0 2345:2345
```
