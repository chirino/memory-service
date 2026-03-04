package com.example.demo;

import java.util.Map;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
class ReadyController {

    @GetMapping("/ready")
    Map<String, String> ready() {
        return Map.of("status", "ok");
    }
}
