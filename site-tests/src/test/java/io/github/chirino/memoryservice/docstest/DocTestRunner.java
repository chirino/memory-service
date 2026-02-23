package io.github.chirino.memoryservice.docstest;

import static io.cucumber.junit.platform.engine.Constants.GLUE_PROPERTY_NAME;
import static io.cucumber.junit.platform.engine.Constants.PLUGIN_PROPERTY_NAME;

import org.junit.platform.suite.api.ConfigurationParameter;
import org.junit.platform.suite.api.IncludeEngines;
import org.junit.platform.suite.api.SelectClasspathResource;
import org.junit.platform.suite.api.Suite;

/**
 * Cucumber test runner for documentation tests.
 *
 * Runs all .feature files generated from documentation test scenarios.
 */
@Suite
@IncludeEngines("cucumber")
@SelectClasspathResource("features")
@ConfigurationParameter(
        key = GLUE_PROPERTY_NAME,
        value = "io.github.chirino.memoryservice.docstest.steps")
@ConfigurationParameter(
        key = PLUGIN_PROPERTY_NAME,
        value =
                "progress, html:target/cucumber-reports/cucumber.html,"
                        + " json:target/cucumber-reports/cucumber.json")
public class DocTestRunner {}
