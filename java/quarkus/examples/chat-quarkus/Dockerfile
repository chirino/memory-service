FROM docker.io/library/node:22-alpine AS frontend-builder

WORKDIR /build
COPY frontends/chat-frontend/package.json .
COPY frontends/chat-frontend/package-lock.json .
RUN npm install

COPY frontends/chat-frontend .
RUN npm run build

FROM maven:3.9.9-eclipse-temurin-21 AS build

WORKDIR /build

# Download mvn dependencies to speed up subsequent builds.
COPY java/mvnw ./java/mvnw
COPY java/.mvn ./java/.mvn

COPY ./java/chat-frontend/pom.xml ./java/chat-frontend/pom.xml
COPY ./java/memory-service-contracts/pom.xml ./java/memory-service-contracts/pom.xml
COPY ./java/pom.xml ./java/pom.xml
COPY ./java/quarkus/examples/chat-quarkus/pom.xml ./java/quarkus/examples/chat-quarkus/pom.xml
COPY ./java/quarkus/examples/pom.xml ./java/quarkus/examples/pom.xml
COPY ./java/quarkus/memory-service-extension/deployment/pom.xml ./java/quarkus/memory-service-extension/deployment/pom.xml
COPY ./java/quarkus/memory-service-extension/pom.xml ./java/quarkus/memory-service-extension/pom.xml
COPY ./java/quarkus/memory-service-extension/runtime/pom.xml ./java/quarkus/memory-service-extension/runtime/pom.xml
COPY ./java/quarkus/memory-service-proto-quarkus/pom.xml ./java/quarkus/memory-service-proto-quarkus/pom.xml
COPY ./java/quarkus/memory-service-rest-quarkus/pom.xml ./java/quarkus/memory-service-rest-quarkus/pom.xml
COPY ./java/quarkus/pom.xml ./java/quarkus/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/deployment/pom.xml ./java/quarkus/quarkus-data-encryption/deployment/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/pom.xml ./java/quarkus/quarkus-data-encryption/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/pom.xml ./java/quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/quarkus-data-encryption-vault/pom.xml ./java/quarkus/quarkus-data-encryption/quarkus-data-encryption-vault/pom.xml
COPY ./java/quarkus/quarkus-data-encryption/runtime/pom.xml ./java/quarkus/quarkus-data-encryption/runtime/pom.xml
COPY ./java/spring/examples/chat-spring/pom.xml ./java/spring/examples/chat-spring/pom.xml
COPY ./java/spring/examples/pom.xml ./java/spring/examples/pom.xml
COPY ./java/spring/memory-service-proto-spring/pom.xml ./java/spring/memory-service-proto-spring/pom.xml
COPY ./java/spring/memory-service-rest-spring/pom.xml ./java/spring/memory-service-rest-spring/pom.xml
COPY ./java/spring/memory-service-spring-boot-autoconfigure/pom.xml ./java/spring/memory-service-spring-boot-autoconfigure/pom.xml
COPY ./java/spring/memory-service-spring-boot-docker-compose-starter/pom.xml ./java/spring/memory-service-spring-boot-docker-compose-starter/pom.xml
COPY ./java/spring/memory-service-spring-boot-starter/pom.xml ./java/spring/memory-service-spring-boot-starter/pom.xml
COPY ./java/spring/pom.xml ./java/spring/pom.xml
RUN ./java/mvnw -B -q -f java/pom.xml -Daether.connector.basic.threads=8 -Daether.metadataResolver.threads=8 -Daether.syncContext.named.time=120 quarkus:go-offline

# Copy the frontend build into the chat app resources
COPY --chmod=755 --from=frontend-builder /build/dist ./java/quarkus/examples/chat-quarkus/src/main/resources/META-INF/resources

# Copy the source code and build the app
COPY . .
RUN ./java/mvnw -B -q -f java/pom.xml -am -pl :memory-service-extension-deployment,:chat-quarkus clean install -DskipTests

FROM registry.access.redhat.com/ubi9/openjdk-21:1.23

ENV LANGUAGE='en_US:en'

# Copy the built demo chat application from build stage
COPY --from=build --chown=185 /build/java/quarkus/examples/chat-quarkus/target/quarkus-app/lib/ /deployments/lib/
COPY --from=build --chown=185 /build/java/quarkus/examples/chat-quarkus/target/quarkus-app/*.jar /deployments/
COPY --from=build --chown=185 /build/java/quarkus/examples/chat-quarkus/target/quarkus-app/app/ /deployments/app/
COPY --from=build --chown=185 /build/java/quarkus/examples/chat-quarkus/target/quarkus-app/quarkus/ /deployments/quarkus/

EXPOSE 8080
USER 185
ENV JAVA_OPTS_APPEND="-Dquarkus.http.host=0.0.0.0 -Djava.util.logging.manager=org.jboss.logmanager.LogManager"
ENV JAVA_APP_JAR="/deployments/quarkus-run.jar"

ENTRYPOINT [ "/opt/jboss/container/java/run/run-java.sh" ]
