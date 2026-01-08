package io.github.chirino.memory.cucumber;

import io.github.chirino.memory.MongoTestProfile;
import io.quarkiverse.cucumber.CucumberQuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.security.TestSecurity;

@TestSecurity(user = "alice")
@TestProfile(MongoTestProfile.class)
public class MongoCucumberTest extends CucumberQuarkusTest {
    public static void main(String[] args) {
        runMain(MongoCucumberTest.class, args);
    }
}
