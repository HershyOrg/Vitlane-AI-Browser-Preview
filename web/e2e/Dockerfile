FROM mcr.microsoft.com/playwright:v1.61.1-noble

WORKDIR /e2e

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/design-lab.html web/index.html web/vite.config.ts web/tsconfig.json web/tsconfig.app.json web/tsconfig.node.json ./
# Both entry documents load /appearance-init.js before anything renders, so the
# screenshot suites need public/ as well. Without it every captured page 404s on
# its own theme bootstrap and paints before data-theme is set.
COPY web/public ./public
COPY web/src ./src
COPY web/e2e ./e2e
COPY shared/openapi/fixtures/curation-step2.v1.json /shared/openapi/fixtures/curation-step2.v1.json
COPY shared/openapi/fixtures/curation-step4.v1.json /shared/openapi/fixtures/curation-step4.v1.json
# styles.css deliberately consumes the same brand-token source as the
# production application build. Keep that source outside /e2e so the relative
# ../../marketing/assets import resolves exactly as it does in the app image.
COPY marketing/assets /marketing/assets

CMD ["npm", "run", "test:e2e:browser"]
