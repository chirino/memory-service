package io.github.chirino.memory.cucumber;

import io.github.chirino.memory.PostgresqlInfinispanS3TestProfile;
import io.quarkiverse.cucumber.CucumberOptions;
import io.quarkiverse.cucumber.CucumberQuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.security.TestSecurity;

@TestSecurity(user = "alice")
@TestProfile(PostgresqlInfinispanS3TestProfile.class)
@CucumberOptions(
        features = {"classpath:features/attachments-rest.feature"},
        tags = "not @direct-stream-only",
        plugin = {
            "pretty",
            "html:target/cucumber-reports/postgresql-infinispan-s3.html",
            "json:target/cucumber-reports/postgresql-infinispan-s3.json"
        })
public class PostgresqlInfinispanS3CucumberTest extends CucumberQuarkusTest {
    public static void main(String[] args) {
        runMain(PostgresqlInfinispanS3CucumberTest.class, args);
    }
}
