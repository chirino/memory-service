package io.github.chirino.memory.resumer;

final class NoopResponseRecorder implements ResponseResumerBackend.ResponseRecorder {

    @Override
    public void record(String token) {}

    @Override
    public void complete() {}
}
