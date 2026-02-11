FROM maven:3.9.9-eclipse-temurin-21 AS build

WORKDIR /build

# Download mvn dependencies to speed up subsequent builds.
COPY mvnw .
COPY .mvn .mvn
COPY pom.xml .
COPY memory-service-contracts/pom.xml memory-service-contracts/
COPY memory-service/pom.xml memory-service/
COPY quarkus/pom.xml quarkus/
COPY quarkus/memory-service-rest-quarkus/pom.xml quarkus/memory-service-rest-quarkus/
COPY quarkus/memory-service-proto-quarkus/pom.xml quarkus/memory-service-proto-quarkus/
COPY quarkus/quarkus-data-encryption/pom.xml quarkus/quarkus-data-encryption/
COPY quarkus/quarkus-data-encryption/runtime/pom.xml quarkus/quarkus-data-encryption/runtime/
COPY quarkus/quarkus-data-encryption/deployment/pom.xml quarkus/quarkus-data-encryption/deployment/
COPY quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/pom.xml quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/
COPY quarkus/quarkus-data-encryption/quarkus-data-encryption-vault/pom.xml quarkus/quarkus-data-encryption/quarkus-data-encryption-vault/
RUN ./mvnw -B -q -Pmemory-service-only -P\!all-modules quarkus:go-offline

# Copy the source code and build the app
COPY . .
RUN ./mvnw -T 1C -B -q -Pmemory-service-only -P\!all-modules -pl memory-service -am clean package -DskipTests -Dquarkus.datasource.jdbc.url=jdbc:postgresql://localhost:5432/memory_service

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
    
