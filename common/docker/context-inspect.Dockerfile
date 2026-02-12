#
# Use this Dockerfile to inspect the context of a build. It will print the content of the context to the console.
#
FROM busybox

RUN mkdir /tmp/build/
# Add context to /tmp/build/
COPY . /tmp/build/
CMD [ "sh", "-c", "cd /tmp/build/ && find ." ]