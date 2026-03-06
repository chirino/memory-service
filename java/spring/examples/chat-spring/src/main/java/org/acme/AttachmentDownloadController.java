package org.acme;

import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * Unauthenticated proxy that forwards signed download requests to the memory-service. The signed
 * token in the URL path provides authorization — no bearer token is required.
 */
@RestController
@RequestMapping("/v1/attachments/download")
class AttachmentDownloadController {

    private final MemoryServiceProxy proxy;

    AttachmentDownloadController(MemoryServiceProxy proxy) {
        this.proxy = proxy;
    }

    @GetMapping("/{token}/{filename}")
    public ResponseEntity<?> download(@PathVariable String token, @PathVariable String filename) {
        return proxy.downloadAttachmentByToken(token, filename);
    }
}
