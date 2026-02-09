package io.github.chirino.memoryservice.docstest;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

/**
 * Main entry point for generating Cucumber feature files from test scenarios JSON.
 * Called by exec-maven-plugin during generate-test-resources phase.
 */
public class TestGeneratorMain {

    public static void main(String[] args) throws Exception {
        if (args.length < 2) {
            System.err.println(
                    "Usage: TestGeneratorMain <test-scenarios.json> <output-features-dir>");
            System.exit(1);
        }

        Path jsonFile = Path.of(args[0]);
        Path outputDir = Path.of(args[1]);

        System.out.println("Working directory: " + System.getProperty("user.dir"));
        System.out.println("Looking for test scenarios at: " + jsonFile.toAbsolutePath());

        if (!Files.exists(jsonFile)) {
            System.out.println("No test scenarios file found at: " + jsonFile);
            System.out.println(
                    "Skipping feature generation (no TestScenario components in docs yet)");
            return;
        }

        System.out.println("Loading test scenarios from: " + jsonFile);
        TestScenarioLoader loader = new TestScenarioLoader();
        List<TestScenarioLoader.TestScenarioData> scenarios = loader.loadScenarios(jsonFile);

        if (scenarios.isEmpty()) {
            System.out.println("No test scenarios found. Skipping feature generation.");
            return;
        }

        System.out.println("Found " + scenarios.size() + " test scenario(s)");

        // Generate a single feature file with all scenarios
        TestGenerator generator = new TestGenerator();
        Path featureFile = outputDir.resolve("documentation-tests.feature");
        generator.generateFeatureFile(scenarios, featureFile);

        System.out.println("Successfully generated feature file: " + featureFile);
    }
}
