# Event bus

This is the part of the [provider path to microservices](https://docs.google.com/document/d/1fYasLvDtyWBUrcOLDliRhVBUxtXZwfK3QEzO4dZgOn4)

## Current state

Current implementation of the PUBSUB is using Go channels and sending native types which limits it's usage to single process app only.
No routing or filtering implemented. I.e. subscriber receives all emitted events and must do filtering on it's own.

## Challenges

### Compatibility
Introduced changes should not break existing codebase i.e. for now it should change implementation only.

### Serialization
Redis example below show use of JSON. Protobuf should work as well.

## MQ Compare

|        Name | Builtin message broker | Persistence | Easy wildcard subscriptions |                   Example Sample                   |
|------------:|:----------------------:|:-----------:|:---------------------------:|:--------------------------------------------------:|
|      ZeroMQ |         **No**         |   **No**    |           **No**            |                        N/A                         |
|    RabbitMQ |        **Yes**         |   **Yes**   |           **Yes**           |                        N/A                         |
|    ActiveMQ |        **Yes**         |   **Yes**   |           **Yes**           |                        N/A                         |
| Apache Qpid |         **No**         |   **No**    |           **No**            |                        N/A                         |
|       Redis |      **Partial**       |   **No**    |           **Yes**           | [redis](https://github.com/ovrclk/akash/pull/1623) |

Both **ZeroMQ** and **Qpid** are rather toolkits and requiring significant time to design and implement routing. Consider them as combination of TCP/UDP on steroids.

## Implementation stage

### Stage 1 - substitution
With the API unchanged replace existing implementation with selected message queue. All existing test SHOULD PASS. 
Subscriber automatically subscribed to all events.

### Stage 2 - API enhancement

1. Add subscribe to topic
2. Add unsubscribe to topic
3. Separate subscriber from the Event bus and rename latter to the Publisher


#### Filtering & routing
Subscriber should be able to make wildcard subscriptions as well as for dedicated events.

Following schema can be used:

`*` indicates a wildcard. multiple wildcard symbols allowed
`|` indicates OR. applied only to characters on the either side of the operator. i.e. subscribing with filter `ab|cd` results in messages from topics `abc` or `acd`   

In the Redis example the routing and filtering based on the type path in the Go package:
Deployment events for instance:
- v1beta2.EventDeploymentCreated
- v1beta2.EventDeploymentUpdated
- v1beta2.EventOrderCreated
- v1beta2.EventOrderClosed
- v1beta1.EventDeploymentCreated
- v1beta1.EventDeploymentUpdated
- v1beta1.EventOrderCreated
- v1beta1.EventOrderClosed

Filter examples
- `*` all 8 events will be received
- `v1beta2.*` - only events starting with prefix `v1beta2.`
  - v1beta2.EventDeploymentCreated
  - v1beta2.EventDeploymentUpdated
  - v1beta2.EventOrderCreated
  - v1beta2.EventOrderClosed

- `v1beta1|2.EventDeployment*` - only deployment events
  - v1beta2.EventDeploymentCreated
  - v1beta2.EventDeploymentUpdated
  - v1beta1.EventDeploymentCreated
  - v1beta1.EventDeploymentUpdated
