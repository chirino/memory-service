package org.acme;

import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import java.util.Map;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RequestPart;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.multipart.MultipartFile;

/**
 * Spring REST controller that proxies attachment requests to the memory-service.
 * Delegates all logic to {@link MemoryServiceProxy}.
 */
@RestController
@RequestMapping("/v1/attachments")
class AttachmentsController {

    private final MemoryServiceProxy proxy;

    AttachmentsController(MemoryServiceProxy proxy) {
        this.proxy = proxy;
    }

    @PostMapping(consumes = MediaType.MULTIPART_FORM_DATA_VALUE)
    public ResponseEntity<?> upload(
            @RequestPart("file") MultipartFile file,
            @RequestParam(value = "expiresIn", required = false) String expiresIn) {
        return proxy.uploadAttachment(file, expiresIn);
    }

    @PostMapping(consumes = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<?> createFromUrl(@RequestBody Map<String, Object> request) {
        return proxy.createAttachmentFromUrl(request);
    }

    @GetMapping("/{id}")
    public ResponseEntity<?> retrieve(@PathVariable String id) {
        return proxy.retrieveAttachment(id);
    }

    @GetMapping("/{id}/download-url")
    public ResponseEntity<?> getDownloadUrl(@PathVariable String id) {
        return proxy.getAttachmentDownloadUrl(id);
    }

    @DeleteMapping("/{id}")
    public ResponseEntity<?> delete(@PathVariable String id) {
        return proxy.deleteAttachment(id);
    }
}
