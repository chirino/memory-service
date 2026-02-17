package io.github.chirino.memory.config;

import jakarta.enterprise.inject.Instance;
import jakarta.enterprise.util.TypeLiteral;
import java.lang.annotation.Annotation;
import java.util.Iterator;
import java.util.List;

/** Minimal {@link Instance} wrapper for unit tests that returns a fixed value from {@link #get()}. */
public class TestInstance<T> implements Instance<T> {

    private final T value;
    private final boolean unsatisfied;

    private TestInstance(T value, boolean unsatisfied) {
        this.value = value;
        this.unsatisfied = unsatisfied;
    }

    public static <T> Instance<T> of(T value) {
        return new TestInstance<>(value, false);
    }

    public static <T> Instance<T> unsatisfied() {
        return new TestInstance<>(null, true);
    }

    @Override
    public T get() {
        if (unsatisfied) {
            throw new IllegalStateException("Unsatisfied instance");
        }
        return value;
    }

    // -- remaining Instance methods delegate back to this instance --

    @Override
    public Instance<T> select(Annotation... qualifiers) {
        return this;
    }

    @SuppressWarnings("unchecked")
    @Override
    public <U extends T> Instance<U> select(Class<U> subtype, Annotation... qualifiers) {
        return (Instance<U>) this;
    }

    @SuppressWarnings("unchecked")
    @Override
    public <U extends T> Instance<U> select(TypeLiteral<U> subtype, Annotation... qualifiers) {
        return (Instance<U>) this;
    }

    @Override
    public boolean isUnsatisfied() {
        return unsatisfied;
    }

    @Override
    public boolean isAmbiguous() {
        return false;
    }

    @Override
    public boolean isResolvable() {
        return !unsatisfied;
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
        if (unsatisfied) {
            return List.<T>of().iterator();
        }
        return List.of(value).iterator();
    }
}
