package example.agent;

import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/v1/ownership-transfers")
class OwnershipTransfersProxyController {

    private final MemoryServiceProxy proxy;

    OwnershipTransfersProxyController(MemoryServiceProxy proxy) {
        this.proxy = proxy;
    }

    @GetMapping
    public ResponseEntity<?> listPendingTransfers(
            @RequestParam(value = "role", required = false) String role) {
        return proxy.listPendingTransfers(role);
    }

    @PostMapping
    public ResponseEntity<?> createOwnershipTransfer(@RequestBody String body) {
        return proxy.createOwnershipTransfer(body);
    }

    @GetMapping("/{transferId}")
    public ResponseEntity<?> getTransfer(@PathVariable String transferId) {
        return proxy.getTransfer(transferId);
    }

    @DeleteMapping("/{transferId}")
    public ResponseEntity<?> deleteTransfer(@PathVariable String transferId) {
        return proxy.deleteTransfer(transferId);
    }

    @PostMapping("/{transferId}/accept")
    public ResponseEntity<?> acceptTransfer(@PathVariable String transferId) {
        return proxy.acceptTransfer(transferId);
    }
}
