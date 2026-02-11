package io.github.chirino.memory.cucumber;

import io.github.chirino.memory.MongoRedisTestProfile;
import io.quarkiverse.cucumber.CucumberOptions;
import io.quarkiverse.cucumber.CucumberQuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.security.TestSecurity;

@TestSecurity(user = "alice")
@TestProfile(MongoRedisTestProfile.class)
@CucumberOptions(
        features = {"classpath:features", "classpath:features-mongodb", "classpath:features-redis"},
        plugin = {
            "pretty",
            "html:target/cucumber-reports/mongo-redis.html",
            "json:target/cucumber-reports/mongo-redis.json"
        })
public class MongoRedisCucumberTest extends CucumberQuarkusTest {
    public static void main(String[] args) {
        runMain(MongoRedisCucumberTest.class, args);
    }
}
