package io.github.chirino.memory.resumer;

public class ResponseResumerRedirectException extends RuntimeException {
    private final AdvertisedAddress target;

    public ResponseResumerRedirectException(AdvertisedAddress target) {
        super(
                target == null
                        ? "Response resumer redirect requested"
                        : "Response resumer redirect to " + target.authority());
        this.target = target;
    }

    public AdvertisedAddress target() {
        return target;
    }
}
