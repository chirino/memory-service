package example;

import io.github.chirino.memory.history.runtime.IndexedContentProvider;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
public class PassThroughIndexedContentProvider implements IndexedContentProvider {

    @Override
    public String getIndexedContent(String text, String role) {
        return text;
    }
}
