package example.agent;

import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.annotation.Import;
import org.springframework.test.context.TestPropertySource;

@SpringBootTest
@Import(TestSecurityConfig.class)
@TestPropertySource(properties = "spring.ai.openai.api-key=test")
class AgentSpringApplicationTests {

    @Test
    void contextLoads() {}
}
