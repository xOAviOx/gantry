FROM node:20-alpine

WORKDIR /app

# No dependencies to install (core http only) — copy the source and run.
COPY package.json ./
COPY server.js ./

ENV PORT=3000
EXPOSE 3000

CMD ["node", "server.js"]
