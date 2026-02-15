package io.github.chirino.memory.config;

import jakarta.enterprise.inject.Instance;
import jakarta.enterprise.util.TypeLiteral;
import java.lang.annotation.Annotation;
import java.util.Iterator;
import java.util.List;

/** Minimal {@link Instance} wrapper for unit tests that returns a fixed value from {@link #get()}. */
class TestInstance<T> implements Instance<T> {

    private final T value;

    private TestInstance(T value) {
        this.value = value;
    }

    static <T> Instance<T> of(T value) {
        return new TestInstance<>(value);
    }

    @Override
    public T get() {
        return value;
    }

    // -- remaining Instance methods are unused in tests --

    @Override
    public Instance<T> select(Annotation... qualifiers) {
        throw new UnsupportedOperationException();
    }

    @Override
    public <U extends T> Instance<U> select(Class<U> subtype, Annotation... qualifiers) {
        throw new UnsupportedOperationException();
    }

    @Override
    public <U extends T> Instance<U> select(TypeLiteral<U> subtype, Annotation... qualifiers) {
        throw new UnsupportedOperationException();
    }

    @Override
    public boolean isUnsatisfied() {
        return false;
    }

    @Override
    public boolean isAmbiguous() {
        return false;
    }

    @Override
    public boolean isResolvable() {
        return true;
    }

    @Override
    public void destroy(T instance) {}

    @Override
    public Handle<T> getHandle() {
        throw new UnsupportedOperationException();
    }

    @Override
    public Iterable<? extends Handle<T>> handles() {
        throw new UnsupportedOperationException();
    }

    @Override
    public Iterator<T> iterator() {
        return List.of(value).iterator();
    }
}
