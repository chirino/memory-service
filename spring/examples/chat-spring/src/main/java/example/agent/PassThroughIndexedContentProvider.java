package example.agent;

import io.github.chirino.memoryservice.history.IndexedContentProvider;
import org.springframework.stereotype.Component;

@Component
public class PassThroughIndexedContentProvider implements IndexedContentProvider {

    @Override
    public String getIndexedContent(String text, String role) {
        return text;
    }
}
