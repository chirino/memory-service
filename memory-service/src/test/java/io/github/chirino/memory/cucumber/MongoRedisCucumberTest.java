package io.github.chirino.memory.cucumber;

import io.github.chirino.memory.MongoRedisTestProfile;
import io.quarkiverse.cucumber.CucumberQuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.security.TestSecurity;

@TestSecurity(user = "alice")
@TestProfile(MongoRedisTestProfile.class)
public class MongoRedisCucumberTest extends CucumberQuarkusTest {
    public static void main(String[] args) {
        runMain(MongoRedisCucumberTest.class, args);
    }
}
