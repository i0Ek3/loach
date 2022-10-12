# loach

The purpose of loach in nature is to balance the ecology and relax the soil. Similarly, loach is also designed as a load balancer to route requests. In loach, you can use three load balancing strategies RR, WRR, and LC to route requests to different backends and return the result to the original client.

## Getting Started

### Build & Run

```Shell
$ go build loach.go
$ ./loach -list=http://localhost:24023,http://localhost:24024,http://localhost:24025,http://localhost:24026
```

Or just run `make`.


## Credit

- https://github.com/kasvith
