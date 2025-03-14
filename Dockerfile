FROM alpine:3.19

# Create a non-root user
RUN addgroup -S app && adduser -S -G app app

# Copy the binary
COPY hass2ch /usr/local/bin/

# Switch to non-root user
USER app

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/hass2ch"]
CMD ["pipeline"]