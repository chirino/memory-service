package io.github.chirino.memory.grpc;

import com.google.protobuf.Empty;
import io.github.chirino.memory.grpc.v1.HealthResponse;
import io.github.chirino.memory.grpc.v1.SystemService;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;

@GrpcService
@Blocking
public class SystemGrpcService implements SystemService {

    @Override
    public Uni<HealthResponse> getHealth(Empty request) {
        return Uni.createFrom().item(() -> HealthResponse.newBuilder().setStatus("ok").build());
    }
}
