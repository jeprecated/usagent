FROM node:24-slim AS runtime

ENV NODE_ENV=production \
    USAGENT_CONFIG=/etc/usagent/config.yaml \
    USAGENT_HOST=0.0.0.0 \
    USAGENT_PORT=8787

WORKDIR /app
COPY package.json ./
COPY src ./src
COPY config.example.yaml /etc/usagent/config.yaml

RUN useradd --system --uid 10001 --create-home usagent
USER usagent

EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD node -e "fetch('http://127.0.0.1:8787/healthz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["node", "src/bin/usagent.mjs"]
