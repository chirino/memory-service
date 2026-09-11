package io.github.chirino.memory.runtime;

import static org.assertj.core.api.Assertions.assertThat;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class UnixSocketMetadataQueryTest {

    @Test
    void twoMetadataExpressionsEncodeAsRepeatedParams() {
        Map<String, Object> params = new LinkedHashMap<>();
        params.put("mode", "all");
        params.put("metadata", List.of("status=waiting", "agent-id=worker-1"));

        String result = UnixSocketHttpClient.appendQuery("/v1/conversations", params);

        assertThat(result)
                .isEqualTo(
                        "/v1/conversations?mode=all&metadata=status%3Dwaiting&metadata=agent-id%3Dworker-1");
    }

    @Test
    void singleMetadataExpressionEncodesAsSingleParam() {
        Map<String, Object> params = new LinkedHashMap<>();
        params.put("metadata", List.of("status!=running"));

        String result = UnixSocketHttpClient.appendQuery("/v1/conversations", params);

        assertThat(result).isEqualTo("/v1/conversations?metadata=status%21%3Drunning");
    }

    @Test
    void emptyMetadataListProducesNoParam() {
        Map<String, Object> params = new LinkedHashMap<>();
        params.put("mode", "all");
        params.put("metadata", List.of());

        String result = UnixSocketHttpClient.appendQuery("/v1/conversations", params);

        assertThat(result).isEqualTo("/v1/conversations?mode=all");
    }
}
