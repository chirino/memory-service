package io.github.chirino.memoryservice.docstest.steps;

import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

import io.cucumber.java.After;
import io.cucumber.java.en.Given;
import io.cucumber.java.en.Then;
import io.cucumber.java.en.When;
import java.io.File;
import java.io.IOException;
import java.net.Socket;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.TimeUnit;

public class CheckpointSteps {

    private Process checkpointProcess;
    private String checkpointPath;
    private String checkpointId;
    private boolean recordingThisCheckpoint;
    private int lastExitCode;
    private int currentPort;
    private static File projectRoot;
    private static final List<ProcessInfo> allProcesses = new ArrayList<>();

    static {
        // Find project root by looking for pom.xml with site-tests module
        projectRoot = findProjectRoot();

        // Add JVM shutdown hook to kill any remaining processes
        Runtime.getRuntime()
                .addShutdownHook(
                        new Thread(
                                () -> {
                                    System.out.println(
                                            "JVM shutdown - cleaning up "
                                                    + allProcesses.size()
                                                    + " processes");
                                    synchronized (allProcesses) {
                                        for (ProcessInfo info : allProcesses) {
                                            killProcess(info.process, info.pid, info.port);
                                        }
                                        allProcesses.clear();
                                    }
                                }));
    }

    private static class ProcessInfo {
        final Process process;
        final long pid;
        final int port;

        ProcessInfo(Process process, long pid, int port) {
            this.process = process;
            this.pid = pid;
            this.port = port;
        }
    }

    @After
    public void cleanupAfterScenario() {
        // Clean up any processes that weren't explicitly stopped
        if (checkpointProcess != null && checkpointProcess.isAlive()) {
            System.out.println("Cleaning up checkpoint process on port " + currentPort);
            stopCheckpoint();
        }
    }

    private static File findProjectRoot() {
        File current = new File(System.getProperty("user.dir"));

        // Search upwards for the root pom.xml
        while (current != null) {
            File pomFile = new File(current, "pom.xml");
            if (pomFile.exists()) {
                // Check if this is the root (has site-tests as a subdirectory)
                File siteTests = new File(current, "site-tests");
                if (siteTests.exists() && siteTests.isDirectory()) {
                    return current;
                }
            }
            current = current.getParentFile();
        }

        // Fallback: assume we're in site-tests and go up one level
        File siteTestsDir = new File(System.getProperty("user.dir"));
        if (siteTestsDir.getName().equals("site-tests")) {
            return siteTestsDir.getParentFile();
        }

        throw new RuntimeException("Could not find project root directory");
    }

    @Given("I have checkpoint {string}")
    public void setCheckpoint(String checkpoint) {
        this.checkpointPath = new File(projectRoot, checkpoint).getAbsolutePath();
        this.checkpointId = checkpoint;
        assertTrue(
                Files.exists(Paths.get(checkpointPath)),
                "Checkpoint directory does not exist: " + checkpoint);
    }

    @When("I build the checkpoint")
    public void buildCheckpoint() throws Exception {
        // Use root mvnw with -f flag to build checkpoint
        File mvnw = new File(projectRoot, "mvnw");
        String pomFile = Paths.get(checkpointPath, "pom.xml").toString();
        ProcessBuilder pb =
                new ProcessBuilder(
                        mvnw.getAbsolutePath(), "clean", "package", "-DskipTests", "-f", pomFile);
        pb.directory(projectRoot);
        pb.redirectErrorStream(true);

        Process process = pb.start();
        lastExitCode = process.waitFor();

        if (lastExitCode != 0) {
            String output = new String(process.getInputStream().readAllBytes());
            fail("Build failed:\n" + output);
        }
    }

    @When("I build the checkpoint with {string}")
    public void buildCheckpoint(String buildCommand) throws Exception {
        File mvnw = new File(projectRoot, "mvnw");
        String pomFile = Paths.get(checkpointPath, "pom.xml").toString();
        String[] cmdParts = buildCommand.split(" ");

        // Replace ./mvnw or mvnw with absolute path
        if (cmdParts[0].equals("./mvnw") || cmdParts[0].equals("mvnw")) {
            cmdParts[0] = mvnw.getAbsolutePath();
        }

        String[] fullCmd = new String[cmdParts.length + 2];
        System.arraycopy(cmdParts, 0, fullCmd, 0, cmdParts.length);
        fullCmd[cmdParts.length] = "-f";
        fullCmd[cmdParts.length + 1] = pomFile;

        ProcessBuilder pb = new ProcessBuilder(fullCmd);
        pb.directory(projectRoot);
        pb.redirectErrorStream(true);

        Process process = pb.start();
        lastExitCode = process.waitFor();

        if (lastExitCode != 0) {
            String output = new String(process.getInputStream().readAllBytes());
            fail("Build failed:\n" + output);
        }
    }

    @Then("the build should succeed")
    public void buildShouldSucceed() {
        assertTrue(lastExitCode == 0, "Build failed with exit code: " + lastExitCode);
    }

    @When("I start the checkpoint on port {int}")
    public void startCheckpoint(int port) throws Exception {
        this.currentPort = port;

        // Decide whether to record or play back for this checkpoint
        recordingThisCheckpoint = false;
        DockerSteps.resetWireMockForCheckpoint();

        if (DockerSteps.RECORD_MODE) {
            boolean hasExisting = DockerSteps.hasFixtures(checkpointId);
            if (hasExisting && !DockerSteps.RECORD_ALL) {
                System.out.println(
                        "Fixtures already exist for "
                                + checkpointId
                                + ", using playback (use SITE_TEST_RECORD=all to re-record).");
                DockerSteps.loadFixturesForCheckpoint(checkpointId);
            } else {
                recordingThisCheckpoint = true;
                System.out.println("Recording mode: requests will be proxied to real OpenAI.");
            }
        } else {
            DockerSteps.loadFixturesForCheckpoint(checkpointId);
        }

        // Determine if this is a Quarkus or Spring Boot application
        File targetDir = new File(checkpointPath, "target");
        File quarkusJar = new File(targetDir, "quarkus-app/quarkus-run.jar");
        File jarFile;
        boolean isQuarkus;

        if (quarkusJar.exists()) {
            // Quarkus application
            jarFile = quarkusJar;
            isQuarkus = true;
            System.out.println("Detected Quarkus application");
        } else {
            // Spring Boot application - find the executable JAR
            File[] jars =
                    targetDir.listFiles(
                            (dir, name) -> name.endsWith(".jar") && !name.endsWith("-sources.jar"));

            if (jars == null || jars.length == 0) {
                throw new RuntimeException("No JAR file found in " + targetDir.getAbsolutePath());
            }

            jarFile = jars[0]; // Use the first JAR found
            isQuarkus = false;
            System.out.println("Detected Spring Boot application");
        }

        // Build command with appropriate port configuration
        ProcessBuilder pb;
        if (isQuarkus) {
            pb =
                    new ProcessBuilder(
                            "java",
                            "-Dquarkus.http.port=" + port,
                            "-jar",
                            jarFile.getAbsolutePath());
        } else {
            pb =
                    new ProcessBuilder(
                            "java", "-jar", jarFile.getAbsolutePath(), "--server.port=" + port);
        }

        pb.directory(new File(checkpointPath));

        // Configure OpenAI endpoint
        pb.environment().put("OPENAI_BASE_URL", DockerSteps.getWireMockBaseUrl());
        if (recordingThisCheckpoint) {
            // In recording mode, pass the real API key and a real model name
            String apiKey = System.getenv("OPENAI_API_KEY");
            if (apiKey == null || apiKey.isEmpty()) {
                throw new RuntimeException(
                        "OPENAI_API_KEY must be set when running in recording mode"
                                + " (SITE_TEST_RECORD=true)");
            }
            pb.environment().put("OPENAI_API_KEY", apiKey);
            String model = System.getenv("OPENAI_MODEL");
            pb.environment()
                    .put("OPENAI_MODEL", (model != null && !model.isEmpty()) ? model : "gpt-4o");
        } else {
            pb.environment().put("OPENAI_API_KEY", "not-needed");
            pb.environment().put("OPENAI_MODEL", "mock-gpt-markdown");
        }

        // Redirect output to files for debugging
        File outputDir = new File(projectRoot, "site-tests/target");
        outputDir.mkdirs();
        File outputFile = new File(outputDir, "checkpoint-" + port + ".log");
        pb.redirectOutput(ProcessBuilder.Redirect.appendTo(outputFile));
        pb.redirectErrorStream(true);

        // Log the complete command with environment
        System.out.println("=== Starting Checkpoint ===");
        System.out.println("Command: " + String.join(" ", pb.command()));
        System.out.println("Working directory: " + pb.directory());
        System.out.println("Environment variables:");
        System.out.println("  OPENAI_BASE_URL=" + pb.environment().get("OPENAI_BASE_URL"));
        System.out.println("  OPENAI_API_KEY=" + pb.environment().get("OPENAI_API_KEY"));
        System.out.println("  OPENAI_MODEL=" + pb.environment().get("OPENAI_MODEL"));
        System.out.println("Output log: " + outputFile.getAbsolutePath());
        System.out.println("========================");

        checkpointProcess = pb.start();
        long pid = checkpointProcess.pid();

        // Track this process for cleanup
        synchronized (allProcesses) {
            allProcesses.add(new ProcessInfo(checkpointProcess, pid, port));
        }
        System.out.println("Started process with PID " + pid + " on port " + port);

        // Wait for application to be ready
        waitForPort(port, Duration.ofSeconds(90));

        // Give additional time for Spring Boot context to fully initialize
        System.out.println(
                "Port is open, waiting additional 10 seconds for application context...");
        try {
            Thread.sleep(10000);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new RuntimeException(
                    "Interrupted while waiting for application initialization", e);
        }
    }

    @Then("the application should be running")
    public void applicationShouldBeRunning() {
        assertTrue(
                checkpointProcess != null && checkpointProcess.isAlive(),
                "Application is not running");
    }

    @When("I stop the checkpoint")
    public void stopCheckpoint() {
        // Save fixtures or reset scenarios before stopping
        if (checkpointId != null) {
            if (recordingThisCheckpoint) {
                DockerSteps.saveFixturesFromJournal(checkpointId);
            } else {
                DockerSteps.resetScenarios();
            }
        }

        if (checkpointProcess != null) {
            long pid = checkpointProcess.pid();

            // Remove from tracking list
            synchronized (allProcesses) {
                allProcesses.removeIf(info -> info.pid == pid);
            }

            // Kill the process
            killProcess(checkpointProcess, pid, currentPort);
            checkpointProcess = null;
        }
    }

    private static void killProcess(Process process, long pid, int port) {
        if (process == null || !process.isAlive()) {
            return;
        }

        // Use destroyForcibly() immediately
        process.destroyForcibly();
        try {
            boolean exited = process.waitFor(5, TimeUnit.SECONDS);
            if (!exited) {
                // Process didn't die, use OS kill
                System.out.println("Process " + pid + " didn't exit, using kill -9");
                new ProcessBuilder("kill", "-9", String.valueOf(pid))
                        .start()
                        .waitFor(2, TimeUnit.SECONDS);
            }
        } catch (Exception e) {
            System.err.println("Error stopping process " + pid + ": " + e.getMessage());
        }

        // Wait for port to be released
        waitForPortRelease(port, Duration.ofSeconds(10));
    }

    private static void waitForPortRelease(int port, Duration timeout) {
        Instant deadline = Instant.now().plus(timeout);

        while (Instant.now().isBefore(deadline)) {
            try (Socket socket = new Socket("localhost", port)) {
                // Port still in use, wait and retry
                try {
                    Thread.sleep(500);
                } catch (InterruptedException ie) {
                    Thread.currentThread().interrupt();
                    return; // Port check interrupted, proceed anyway
                }
            } catch (IOException e) {
                // Port is now free
                System.out.println("Port " + port + " has been released");
                return;
            }
        }

        System.out.println(
                "Warning: Port " + port + " may still be in use after waiting " + timeout);
    }

    private void waitForPort(int port, Duration timeout) {
        Instant deadline = Instant.now().plus(timeout);

        while (Instant.now().isBefore(deadline)) {
            try (Socket socket = new Socket("localhost", port)) {
                // Connection successful
                return;
            } catch (IOException e) {
                // Not ready yet, sleep and retry
                try {
                    Thread.sleep(1000);
                } catch (InterruptedException ie) {
                    Thread.currentThread().interrupt();
                    throw new RuntimeException("Interrupted while waiting for application", ie);
                }
            }
        }

        throw new RuntimeException(
                String.format("Application did not start on port %d within %s", port, timeout));
    }
}
