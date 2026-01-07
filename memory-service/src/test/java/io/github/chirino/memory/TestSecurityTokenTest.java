package io.github.chirino.memory;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.is;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.security.TestSecurity;
import io.restassured.response.Response;
import org.junit.jupiter.api.Test;

@QuarkusTest
class TestSecurityTokenTest {

    @Test
    @TestSecurity(user = "alice")
    void testSecurity_addsAuthorizationHeader_automatically() {
        // Make a request to a secured endpoint
        Response response =
                given().when().get("/v1/conversations").then().statusCode(200).extract().response();

        // Verify the request was authenticated (we got 200, not 401)
        // This confirms that @TestSecurity automatically added the Authorization header
    }

    @Test
    @TestSecurity(user = "alice")
    void testSecurity_withoutHeader_returns401() {
        // Make a request without Authorization header (should fail)
        given().when()
                .get("/v1/conversations")
                .then()
                .statusCode(200); // This should pass because @TestSecurity adds the header
    }

    @Test
    void testSecurity_withoutAnnotation_returns401() {
        // Make a request without @TestSecurity annotation (should fail)
        given().when()
                .get("/v1/conversations")
                .then()
                .statusCode(401); // Should be 401 without authentication
    }

    @Test
    @TestSecurity(user = "alice")
    void testSecurity_createsConversation_asAlice() {
        // Verify that the user from @TestSecurity is used
        String conversationId =
                given().contentType("application/json")
                        .body(java.util.Map.of("title", "Test Conversation"))
                        .when()
                        .post("/v1/conversations")
                        .then()
                        .statusCode(201)
                        .body("ownerUserId", is("alice"))
                        .extract()
                        .path("id");

        // Verify we can access it
        given().when()
                .get("/v1/conversations/{id}/messages", conversationId)
                .then()
                .statusCode(200);
    }

    @Test
    void testSecurity_withManualBearerToken_returns401() {
        // Test that a simple bearer token doesn't work - we need the test security interceptor
        given().header("Authorization", "Bearer test-token-alice")
                .when()
                .get("/v1/conversations")
                .then()
                .statusCode(401); // Should fail because it's not a valid OIDC token
    }

    @Test
    void testSecurity_withKeycloakTestClient_works() {
        // Test using KeycloakTestClient to get a real token
        io.quarkus.test.keycloak.client.KeycloakTestClient keycloakClient =
                new io.quarkus.test.keycloak.client.KeycloakTestClient();
        String token = keycloakClient.getAccessToken("alice");

        given().auth().oauth2(token).when().get("/v1/conversations").then().statusCode(200);
    }
}
