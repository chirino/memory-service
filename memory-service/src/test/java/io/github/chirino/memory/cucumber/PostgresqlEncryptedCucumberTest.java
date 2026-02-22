package io.github.chirino.memory.cucumber;

import io.github.chirino.memory.PostgresqlEncryptedTestProfile;
import io.quarkiverse.cucumber.CucumberOptions;
import io.quarkiverse.cucumber.CucumberQuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.security.TestSecurity;

/**
 * Runs encrypted file store integration tests against a PostgreSQL backend with DEK encryption
 * enabled for both MemoryStore data and the file store.
 */
@TestSecurity(user = "alice")
@TestProfile(PostgresqlEncryptedTestProfile.class)
@CucumberOptions(
        features = {"classpath:features-encrypted"},
        plugin = {
            "pretty",
            "html:target/cucumber-reports/postgresql-encrypted.html",
            "json:target/cucumber-reports/postgresql-encrypted.json"
        })
public class PostgresqlEncryptedCucumberTest extends CucumberQuarkusTest {
    public static void main(String[] args) {
        runMain(PostgresqlEncryptedCucumberTest.class, args);
    }
}
