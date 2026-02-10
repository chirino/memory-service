FROM docker.io/library/node:22-alpine AS frontend-builder

WORKDIR /build
COPY common/chat-frontend/package.json .
COPY common/chat-frontend/package-lock.json .
RUN npm install

COPY common/chat-frontend .
RUN npm run build

FROM maven:3.9.9-eclipse-temurin-21 AS build

WORKDIR /build

# Download mvn dependencies to speed up subsequent builds.
COPY mvnw .
COPY .mvn .mvn

COPY ./common/chat-frontend/pom.xml ./common/chat-frontend/pom.xml
COPY ./memory-service-contracts/pom.xml ./memory-service-contracts/pom.xml
COPY ./memory-service/pom.xml ./memory-service/pom.xml
COPY ./pom.xml ./pom.xml
COPY ./quarkus/examples/chat-quarkus/pom.xml ./quarkus/examples/chat-quarkus/pom.xml
COPY ./quarkus/examples/pom.xml ./quarkus/examples/pom.xml
COPY ./quarkus/memory-service-extension/deployment/pom.xml ./quarkus/memory-service-extension/deployment/pom.xml
COPY ./quarkus/memory-service-extension/pom.xml ./quarkus/memory-service-extension/pom.xml
COPY ./quarkus/memory-service-extension/runtime/pom.xml ./quarkus/memory-service-extension/runtime/pom.xml
COPY ./quarkus/memory-service-proto-quarkus/pom.xml ./quarkus/memory-service-proto-quarkus/pom.xml
COPY ./quarkus/memory-service-rest-quarkus/pom.xml ./quarkus/memory-service-rest-quarkus/pom.xml
COPY ./quarkus/pom.xml ./quarkus/pom.xml
COPY ./quarkus/quarkus-data-encryption/deployment/pom.xml ./quarkus/quarkus-data-encryption/deployment/pom.xml
COPY ./quarkus/quarkus-data-encryption/pom.xml ./quarkus/quarkus-data-encryption/pom.xml
COPY ./quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/pom.xml ./quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/pom.xml
COPY ./quarkus/quarkus-data-encryption/quarkus-data-encryption-vault/pom.xml ./quarkus/quarkus-data-encryption/quarkus-data-encryption-vault/pom.xml
COPY ./quarkus/quarkus-data-encryption/runtime/pom.xml ./quarkus/quarkus-data-encryption/runtime/pom.xml
COPY ./spring/examples/chat-spring/pom.xml ./spring/examples/chat-spring/pom.xml
COPY ./spring/examples/pom.xml ./spring/examples/pom.xml
COPY ./spring/memory-service-proto-spring/pom.xml ./spring/memory-service-proto-spring/pom.xml
COPY ./spring/memory-service-rest-spring/pom.xml ./spring/memory-service-rest-spring/pom.xml
COPY ./spring/memory-service-spring-boot-autoconfigure/pom.xml ./spring/memory-service-spring-boot-autoconfigure/pom.xml
COPY ./spring/memory-service-spring-boot-docker-compose-starter/pom.xml ./spring/memory-service-spring-boot-docker-compose-starter/pom.xml
COPY ./spring/memory-service-spring-boot-starter/pom.xml ./spring/memory-service-spring-boot-starter/pom.xml
COPY ./spring/pom.xml ./spring/pom.xml

RUN ./mvnw quarkus:go-offline

# Copy the frontend build into the chat app resources
COPY --chmod=755 --from=frontend-builder /build/dist ./quarkus/examples/chat-quarkus/src/main/resources/META-INF/resources

# Copy the source code and build the app
COPY . .
RUN ./mvnw -B -q -am  -pl :memory-service-extension-deployment,:chat-quarkus clean install -DskipTests

FROM registry.access.redhat.com/ubi9/openjdk-21:1.23

ENV LANGUAGE='en_US:en'

# Copy the built demo chat application from build stage
COPY --from=build --chown=185 /build/quarkus/examples/chat-quarkus/target/quarkus-app/lib/ /deployments/lib/
COPY --from=build --chown=185 /build/quarkus/examples/chat-quarkus/target/quarkus-app/*.jar /deployments/
COPY --from=build --chown=185 /build/quarkus/examples/chat-quarkus/target/quarkus-app/app/ /deployments/app/
COPY --from=build --chown=185 /build/quarkus/examples/chat-quarkus/target/quarkus-app/quarkus/ /deployments/quarkus/

EXPOSE 8080
USER 185
ENV JAVA_OPTS_APPEND="-Dquarkus.http.host=0.0.0.0 -Djava.util.logging.manager=org.jboss.logmanager.LogManager"
ENV JAVA_APP_JAR="/deployments/quarkus-run.jar"

ENTRYPOINT [ "/opt/jboss/container/java/run/run-java.sh" ]
