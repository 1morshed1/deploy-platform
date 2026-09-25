# Build for a static website (plain HTML/CSS/JS, no build step).
#
# We copy the site into an nginx image that serves it. nginx:alpine is tiny and
# battle-tested as a static file server.
FROM nginx:1.27-alpine
# Replace the default nginx site with the user's files.
COPY . /usr/share/nginx/html
EXPOSE 80
# nginx's own default CMD runs the server in the foreground; no override needed.
