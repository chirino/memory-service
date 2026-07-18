# Memory Service Binaries Facts

- The aggregate `memory-service-binaries` JAR contains no behavior or native payload. Keep its public `MemoryServiceBinaries` marker type because a `package-info.java` by itself is not enough for JDK 21 Javadoc generation under the inherited `central-release` profile.
- The `test-binary-jars` CI job activates both `binary-jars` and `central-release`, with GPG signing and Central publishing disabled, so release-only Javadoc and source attachment remain covered before a release.
