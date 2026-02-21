package io.github.chirino.memory.cucumber;

import io.github.chirino.memory.MongoRedisQdrantTestProfile;
import io.github.chirino.memory.QdrantTestResource;
import io.quarkiverse.cucumber.CucumberOptions;
import io.quarkiverse.cucumber.CucumberQuarkusTest;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.security.TestSecurity;

@TestSecurity(user = "alice")
@TestProfile(MongoRedisQdrantTestProfile.class)
@QuarkusTestResource(value = QdrantTestResource.class, restrictToAnnotatedClass = true)
@CucumberOptions(
        features = {"classpath:features-qdrant"},
        plugin = {
            "pretty",
            "html:target/cucumber-reports/mongo-redis-qdrant.html",
            "json:target/cucumber-reports/mongo-redis-qdrant.json"
        })
public class MongoRedisQdrantCucumberTest extends CucumberQuarkusTest {
    public static void main(String[] args) {
        runMain(MongoRedisQdrantCucumberTest.class, args);
    }
}
