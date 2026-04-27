# Plano de implementação

Cada etapa só avança após os testes da etapa atual passarem.
Após cada commit, verificar `read-detalhado-e-duvidas.md` para confirmar cobertura dos requisitos.

## Etapas

- [x] 1 — Scaffold + Infra — go.mod, docker-compose (Postgres, Redis, mock IdP), Dockerfile, justfile
- [x] 2 — Config — internal/config/, fail-fast, .env.example
- [x] 3 — Migrations + Storage — migrations SQL, NotificationRepo, testes de integração com rollback por tx
- [x] 4 — Auth middleware — JWT RS256 + JWKS, extração de CPF do claim preferred_username
- [x] 5 — Webhook handler — validação HMAC-SHA256, payload, citizen_ref, event_hash, persistência
- [x] 6 — Broadcast — Redis pub/sub, publisher e subscriber
- [x] 7 — REST API — GET /notifications, PATCH /notifications/:id/read, GET /notifications/unread-count
- [x] 8 — WebSocket — GET /ws, broadcaster local, múltiplas conexões, heartbeat
- [ ] 9 — README final — como rodar, decisões, o que faria diferente


- [x] 10 - Tracing com OpenTelemetry
- [x] 11 - Load test with k6
- [x] 12 - Dead letter queue para webhooks que falharam na persistência
- [x] 13 - Circuit breaker para dependências externas
- [x] 14 - Manifests Kubernetes


- [x] n - melhorar o readme -> menos verboso, indice com decisões, e índice com melhorias.

- [ ] 15 - Melhorias 1: auth_test.go cria um teste, monta router, será que isso pode ser extraído e compartilhado entre módulos?
- [ ] 16 - mudar para um validador mais robusto a la pydantic/zod
- [x] 17 - testar que consigo rodar tudo dentro do docker
- [ ] 18 - mudar pra property test.
- [ ] 19 - limites/CORS
- [ ] 20 - mudar docker pra sem dist para reduzir uso de recursos e melhor segurança. 