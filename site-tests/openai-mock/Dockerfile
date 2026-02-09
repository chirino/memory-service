FROM wiremock/wiremock:3.11.0

# Copy mappings (request/response stubs)
COPY mappings /home/wiremock/mappings

# Expose WireMock port
EXPOSE 8080

# Run WireMock with verbose logging
ENTRYPOINT ["/docker-entrypoint.sh", "--global-response-templating", "--verbose"]
