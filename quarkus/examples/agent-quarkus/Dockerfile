FROM docker.io/library/node:22-alpine AS frontend-builder

WORKDIR /build
COPY examples/agent-webui/package.json .
COPY examples/agent-webui/package-lock.json .
RUN npm install

COPY examples/agent-webui .
RUN npm run build

FROM maven:3.9.9-eclipse-temurin-21 AS build

WORKDIR /build

# Copy Maven files
COPY pom.xml .
COPY mvnw .
COPY .mvn .mvn

# Copy the frontend build into the agent resources
COPY --chmod=755 --from=frontend-builder /build/dist examples/agent-quarkus/src/main/resources/META-INF/resources

# Copy module sources
COPY memory-service-contracts memory-service-contracts
COPY quarkus quarkus
COPY spring/pom.xml spring/
COPY spring/memory-service-proto-spring/pom.xml spring/memory-service-proto-spring/
COPY examples/pom.xml examples/
COPY examples/agent-quarkus examples/agent-quarkus

# Build the agent application
RUN ./mvnw -B -q -pl examples/agent-quarkus -am clean package -DskipTests

FROM registry.access.redhat.com/ubi9/openjdk-21:1.23

ENV LANGUAGE='en_US:en'

# Copy the built agent application from build stage
COPY --from=build --chown=185 /build/examples/agent-quarkus/target/quarkus-app/lib/ /deployments/lib/
COPY --from=build --chown=185 /build/examples/agent-quarkus/target/quarkus-app/*.jar /deployments/
COPY --from=build --chown=185 /build/examples/agent-quarkus/target/quarkus-app/app/ /deployments/app/
COPY --from=build --chown=185 /build/examples/agent-quarkus/target/quarkus-app/quarkus/ /deployments/quarkus/

EXPOSE 8080
USER 185
ENV JAVA_OPTS_APPEND="-Dquarkus.http.host=0.0.0.0 -Djava.util.logging.manager=org.jboss.logmanager.LogManager"
ENV JAVA_APP_JAR="/deployments/quarkus-run.jar"

ENTRYPOINT [ "/opt/jboss/container/java/run/run-java.sh" ]
