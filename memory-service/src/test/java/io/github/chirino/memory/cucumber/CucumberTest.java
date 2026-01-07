package io.github.chirino.memory.cucumber;

import io.quarkiverse.cucumber.CucumberQuarkusTest;
import io.quarkus.test.security.TestSecurity;

@TestSecurity(user = "alice")
public class CucumberTest extends CucumberQuarkusTest {
    public static void main(String[] args) {
        runMain(CucumberTest.class, args);
    }
}
