package io.github.chirino.memory.attachment;

import java.io.FilterInputStream;
import java.io.IOException;
import java.io.InputStream;

/**
 * An InputStream wrapper that counts bytes read and throws {@link FileStoreException} if the count
 * exceeds the configured maximum size.
 */
public class CountingInputStream extends FilterInputStream {

    private final long maxSize;
    private long count;

    public CountingInputStream(InputStream in, long maxSize) {
        super(in);
        this.maxSize = maxSize;
    }

    @Override
    public int read() throws IOException {
        int b = super.read();
        if (b != -1) {
            count++;
            checkLimit();
        }
        return b;
    }

    @Override
    public int read(byte[] b, int off, int len) throws IOException {
        int n = super.read(b, off, len);
        if (n > 0) {
            count += n;
            checkLimit();
        }
        return n;
    }

    public long getCount() {
        return count;
    }

    private void checkLimit() {
        if (count > maxSize) {
            throw new FileStoreException(maxSize, count);
        }
    }
}
