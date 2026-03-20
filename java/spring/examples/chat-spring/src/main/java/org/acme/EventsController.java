package org.acme;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import org.springframework.http.MediaType;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.mvc.method.annotation.SseEmitter;

@RestController
class EventsController {

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private final MemoryServiceProxy proxy;

    EventsController(MemoryServiceProxy proxy) {
        this.proxy = proxy;
    }

    @GetMapping(value = "/v1/events", produces = MediaType.TEXT_EVENT_STREAM_VALUE)
    SseEmitter streamEvents(@RequestParam(required = false) String kinds) {
        SseEmitter emitter = new SseEmitter(Long.MAX_VALUE);
        proxy.streamEvents(kinds)
                .subscribe(
                        event -> {
                            try {
                                emitter.send(
                                        OBJECT_MAPPER.writeValueAsString(event),
                                        MediaType.APPLICATION_JSON);
                            } catch (Exception e) {
                                emitter.completeWithError(e);
                            }
                        },
                        emitter::completeWithError,
                        emitter::complete);
        return emitter;
    }
}
