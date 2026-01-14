FROM maven:3.9.9-eclipse-temurin-21 AS build

WORKDIR /build

# Copy Maven files
COPY mvnw .
COPY .mvn .mvn

COPY pom.xml .
COPY memory-service/pom.xml memory-service/
COPY memory-service-client/pom.xml memory-service-client/
COPY memory-service-proto/pom.xml memory-service-proto/
COPY agent/pom.xml agent/
COPY memory-service-extension/pom.xml memory-service-extension/
COPY memory-service-extension/runtime/pom.xml memory-service-extension/runtime/
COPY memory-service-extension/deployment/pom.xml memory-service-extension/deployment/
COPY quarkus-data-encryption/pom.xml quarkus-data-encryption/
COPY quarkus-data-encryption/runtime/pom.xml quarkus-data-encryption/runtime/
COPY quarkus-data-encryption/deployment/pom.xml quarkus-data-encryption/deployment/
COPY quarkus-data-encryption/quarkus-data-encryption-dek/pom.xml quarkus-data-encryption/quarkus-data-encryption-dek/
COPY quarkus-data-encryption/quarkus-data-encryption-vault/pom.xml quarkus-data-encryption/quarkus-data-encryption-vault/
RUN ./mvnw -B -q -pl memory-service -am quarkus:go-offline

# Copy all the sources
COPY . .

# Build the service application
RUN ./mvnw -T 1C -B -q -pl memory-service -am clean package -DskipTests -Dquarkus.datasource.jdbc.url=jdbc:postgresql://localhost:5432/memory_service

# Runtime stage
FROM registry.access.redhat.com/ubi9/openjdk-21:1.23

ENV LANGUAGE='en_US:en'

# Copy the built application from build stage
COPY --from=build --chown=185 /build/memory-service/target/quarkus-app/lib/ /deployments/lib/
COPY --from=build --chown=185 /build/memory-service/target/quarkus-app/*.jar /deployments/
COPY --from=build --chown=185 /build/memory-service/target/quarkus-app/app/ /deployments/app/
COPY --from=build --chown=185 /build/memory-service/target/quarkus-app/quarkus/ /deployments/quarkus/

EXPOSE 8080
USER 185
ENV JAVA_OPTS_APPEND="-Dquarkus.http.host=0.0.0.0 -Djava.util.logging.manager=org.jboss.logmanager.LogManager"
ENV JAVA_APP_JAR="/deployments/quarkus-run.jar"

ENTRYPOINT [ "/opt/jboss/container/java/run/run-java.sh" ]
    
