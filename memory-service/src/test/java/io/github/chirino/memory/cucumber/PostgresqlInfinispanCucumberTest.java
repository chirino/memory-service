package io.github.chirino.memory.cucumber;

import io.github.chirino.memory.PostgresqlInfinispanTestProfile;
import io.quarkiverse.cucumber.CucumberOptions;
import io.quarkiverse.cucumber.CucumberQuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.security.TestSecurity;

@TestSecurity(user = "alice")
@TestProfile(PostgresqlInfinispanTestProfile.class)
@CucumberOptions(
        features = {
            "classpath:features",
            "classpath:features/postgres",
            "classpath:features/infinispan"
        })
public class PostgresqlInfinispanCucumberTest extends CucumberQuarkusTest {
    public static void main(String[] args) {
        runMain(PostgresqlInfinispanCucumberTest.class, args);
    }
}
