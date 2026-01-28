package io.github.chirino.memory.cucumber;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.util.Set;
import java.util.concurrent.ConcurrentSkipListSet;

/**
 * Tracks which step methods are used during test execution.
 * Enable tracking by setting TRACK_STEPS=true environment variable.  Example:
 * <p><pre>
 *     TRACK_STEPS=true ./mvnw clean test -pl memory-service
 *     cat ./memory-service/target/cucumber/usedStepMethods.txt
 * </pre><p>
 * Results are written to target/cucumber/usedStepMethods.txt when tests end.
 */
public class StepUsageTracker {

    private static final Set<String> usedStepMethods = new ConcurrentSkipListSet<>();
    private static final boolean ENABLED = "true".equalsIgnoreCase(System.getenv("TRACK_STEPS"));

    static {
        if (ENABLED) {
            Runtime.getRuntime().addShutdownHook(new Thread(StepUsageTracker::writeResults));
        }
    }

    /**
     * Records the calling step method name if TRACK_STEPS=true.
     * Call this at the start of each step method.
     */
    public static void trackUsage() {
        if (!ENABLED) {
            return;
        }
        StackTraceElement[] stackTrace = Thread.currentThread().getStackTrace();
        // stackTrace[0] is getStackTrace, stackTrace[1] is trackUsage, stackTrace[2] is the caller
        if (stackTrace.length > 2) {
            StackTraceElement caller = stackTrace[2];
            String line =
                    String.format(
                            "at %s.%s(%s:%d)",
                            caller.getClassName(),
                            caller.getMethodName(),
                            caller.getFileName(),
                            caller.getLineNumber() - 1);
            usedStepMethods.add(line);
        }
    }

    private static void writeResults() {
        if (usedStepMethods.isEmpty()) {
            return;
        }
        try {
            Path outputDir = Path.of("target/cucumber");
            Files.createDirectories(outputDir);
            Path outputFile = outputDir.resolve("usedStepMethods.txt");
            StringBuilder content = new StringBuilder();
            for (String method : usedStepMethods) {
                content.append(method).append("\n");
            }
            Files.writeString(
                    outputFile,
                    content.toString(),
                    StandardOpenOption.CREATE,
                    StandardOpenOption.APPEND);
        } catch (IOException e) {
            System.err.println("Failed to write step usage results: " + e.getMessage());
        }
    }
}
