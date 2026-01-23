package example.agent;

import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpHeaders;
import org.springframework.stereotype.Component;
import org.springframework.util.StringUtils;
import org.springframework.web.filter.OncePerRequestFilter;

/**
 * Logs basic details about every incoming HTTP request handled by the Spring example app.
 */
@Component
class HttpRequestLoggingFilter extends OncePerRequestFilter {

    private static final Logger LOG = LoggerFactory.getLogger(HttpRequestLoggingFilter.class);

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain filterChain)
            throws ServletException, IOException {

        String method = request.getMethod();
        String uriWithQuery =
                StringUtils.hasText(request.getQueryString())
                        ? request.getRequestURI() + "?" + request.getQueryString()
                        : request.getRequestURI();
        boolean hasAuthorization =
                StringUtils.hasText(request.getHeader(HttpHeaders.AUTHORIZATION));
        boolean hasApiKey = StringUtils.hasText(request.getHeader("X-API-Key"));
        String remote = request.getRemoteAddr();

        filterChain.doFilter(request, response);

        int status = response.getStatus();
        LOG.info(
                "HTTP request: {} {}, status {}, remote {}, Authorization header: {}, X-API-Key"
                        + " header: {}",
                method,
                uriWithQuery,
                status,
                remote,
                hasAuthorization,
                hasApiKey);
    }
}
