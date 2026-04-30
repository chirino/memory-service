FROM maven:3.9.9-eclipse-temurin-21 AS build

WORKDIR /build

# Copy Maven wrapper and POM files for dependency resolution
COPY java/mvnw ./java/mvnw
COPY java/.mvn ./java/.mvn

COPY ./java/memory-service-contracts/pom.xml ./java/memory-service-contracts/pom.xml
COPY ./java/pom.xml ./java/pom.xml
COPY ./java/quarkus/memory-service-rest-quarkus/pom.xml ./java/quarkus/memory-service-rest-quarkus/pom.xml
COPY ./java/quarkus/pom.xml ./java/quarkus/pom.xml
COPY ./java/quarkus/skill-extractor-quarkus/pom.xml ./java/quarkus/skill-extractor-quarkus/pom.xml
# Stub POMs for reactor modules we don't build but need for resolution
COPY ./java/quarkus/memory-service-extension/pom.xml ./java/quarkus/memory-service-extension/pom.xml
COPY ./java/quarkus/memory-service-extension/deployment/pom.xml ./java/quarkus/memory-service-extension/deployment/pom.xml
COPY ./java/quarkus/memory-service-extension/runtime/pom.xml ./java/quarkus/memory-service-extension/runtime/pom.xml
COPY ./java/quarkus/memory-service-proto-quarkus/pom.xml ./java/quarkus/memory-service-proto-quarkus/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/pom.xml ./java/quarkus/quarkus-data-encryption/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/deployment/pom.xml ./java/quarkus/quarkus-data-encryption/deployment/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/runtime/pom.xml ./java/quarkus/quarkus-data-encryption/runtime/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/pom.xml ./java/quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/quarkus-data-encryption-vault/pom.xml ./java/quarkus/quarkus-data-encryption/quarkus-data-encryption-vault/pom.xml
COPY ./java/quarkus/examples/pom.xml ./java/quarkus/examples/pom.xml

# Copy source code and build
COPY . .
RUN ./java/mvnw -B -q -f java/pom.xml -am -pl :skill-extractor-quarkus clean package -DskipTests

FROM registry.access.redhat.com/ubi9/openjdk-21:1.23

ENV LANGUAGE='en_US:en'

COPY --from=build --chown=185 /build/java/quarkus/skill-extractor-quarkus/target/quarkus-app/lib/ /deployments/lib/
COPY --from=build --chown=185 /build/java/quarkus/skill-extractor-quarkus/target/quarkus-app/*.jar /deployments/
COPY --from=build --chown=185 /build/java/quarkus/skill-extractor-quarkus/target/quarkus-app/app/ /deployments/app/
COPY --from=build --chown=185 /build/java/quarkus/skill-extractor-quarkus/target/quarkus-app/quarkus/ /deployments/quarkus/

EXPOSE 8090
USER 185
ENV JAVA_OPTS_APPEND="-Dquarkus.http.host=0.0.0.0 -Djava.util.logging.manager=org.jboss.logmanager.LogManager"
ENV JAVA_APP_JAR="/deployments/quarkus-run.jar"

ENTRYPOINT [ "/opt/jboss/container/java/run/run-java.sh" ]
