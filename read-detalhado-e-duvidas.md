# Desafio Técnico — Análise Detalhada

_Documento-base para implementação por agentes. Mantém a estrutura em checklist e fecha as decisões necessárias para o MVP._

## Contexto

O enunciado envolve **dois sistemas distintos**:

1. **Sistema-fonte (externo, fora do escopo):** sistema da Prefeitura que controla o ciclo de vida do chamado (`aberto → em_analise → em_execucao → concluido`) e emite os webhooks de mudança de status.
2. **Serviço de notificações (o que vamos construir):** consome os webhooks, persiste e entrega ao cidadão via WebSocket (tempo real) e REST (sob demanda).

## Webhook

Endpoint `POST` exposto pelo nosso serviço que recebe do sistema-fonte um evento de mudança de status de chamado (JSON com `chamado_id`, `cpf`, `status_anterior`, `status_novo`, etc.). Cada requisição vem com header `X-Signature-256` contendo `sha256=<HMAC-SHA256(body, secret)>` e é validada antes de processar. O mesmo evento pode chegar duas vezes por retry do emissor; para o MVP, a idempotência será por **hash do body cru**. Após persistir, o evento é propagado para o WebSocket do cidadão correspondente via Redis pub/sub.

## API REST

API autenticada por JWT Bearer. O CPF do cidadão será lido do claim `preferred_username`. Os endpoints do MVP serão `GET /notifications`, `PATCH /notifications/:id/read` e `GET /notifications/unread-count`. Toda consulta e mutação deve ser isolada por cidadão.

## WebSocket

Endpoint `GET /ws` autenticado com o mesmo JWT Bearer usado na REST API. Quando uma nova notificação for persistida, o cidadão conectado deve recebê-la em tempo real. O WebSocket é um canal de baixa latência, não a fonte de verdade; a fonte de verdade continua sendo o banco consultado pela API REST.

## Privacidade

CPF não será persistido em texto. O banco armazenará apenas um identificador derivado determinístico (`cidadao_ref`) calculado com HMAC-SHA256 sobre o CPF usando uma chave interna separada.

## Stack / Entregáveis / Diferenciais

Stack mandatória do MVP: Go 1.24+, Gin, PostgreSQL com SQL direto, Redis, Docker Compose e `just`. Diferenciais do enunciado continuam opcionais e ficam fora do escopo inicial.

---

## Requisitos

### Modelo de Dados

- [ ] **R-DM-1** — Existe uma tabela `notifications` com a seguinte estrutura:

| Coluna | Tipo | Origem / observação |
|---|---|---|
| `id` | UUID (PK) | gerado por nós |
| `chamado_id` | TEXT | do payload (`CH-2024-001234`) |
| `tipo` | TEXT | do payload (`status_change`) |
| `cidadao_ref` | BYTEA | derivado do CPF — não guardamos CPF em claro |
| `status_anterior` | TEXT | do payload (enum validado na lógica de negócio) |
| `status_novo` | TEXT | do payload (enum validado na lógica de negócio) |
| `titulo` | TEXT | do payload |
| `descricao` | TEXT NULL | do payload |
| `event_timestamp` | TIMESTAMPTZ | do payload (`timestamp`) |
| `received_at` | TIMESTAMPTZ DEFAULT NOW() | nosso (quando chegou o webhook) |
| `lida` | BOOLEAN DEFAULT FALSE | nosso (marcada pelo PATCH) |
| `lida_at` | TIMESTAMPTZ NULL | nosso (quando foi marcada) |
| `event_hash` | BYTEA NOT NULL UNIQUE | SHA-256 do body bruto — usado para dedup de eventos idênticos |

- [ ] **R-DM-2** — Constraints **no banco**  (apenas as estruturais; validações de domínio ficam na lógica de negócio):
  - **PK** em `id`.
  - **UNIQUE** em `event_hash`.
  - **NOT NULL** em todos os campos exceto `descricao` e `lida_at`.
  - **DEFAULTs:** `id = gen_random_uuid()`, `received_at = NOW()`, `lida = FALSE`.

- [ ] **R-DM-3** — Índices:
  - `idx_notif_cidadao_ts` sobre `(cidadao_ref, event_timestamp DESC, id DESC)`.
  - `idx_notif_unread` índice **parcial** sobre `(cidadao_ref) WHERE lida = false`  — serve `unread-count` com footprint mínimo.
- [ ] **R-DM-4** — Retry de evento já processado (violação do `UNIQUE event_hash`) responde **`200 OK`** silencioso e não cria novo registro.

### Isolamento por cidadão (global)

- [ ] **R-ISO-1** — Cada notificação pertence a **exatamente um cidadão**, identificado pelo CPF recebido no payload do webhook.
- [ ] **R-ISO-2** — **Webhook:** ao persistir, a notificação é gravada associada ao CPF do payload; nenhum evento pode ser exposto para outro cidadão.
- [ ] **R-ISO-3** — **REST:** toda query filtra pelo CPF extraído do JWT; recurso inexistente ou pertencente a outro cidadão responde `404`.
- [ ] **R-ISO-4** — **WebSocket:** o cidadão conectado recebe **apenas** notificações vinculadas ao próprio CPF.

### Webhook

- [ ] **R-WH-0** — O serviço reconhece os quatro estados possíveis de um chamado: `aberto`, `em_analise`, `em_execucao`, `concluido`.
- [ ] **R-WH-1** — O serviço recebe `POST` de webhook no formato de payload definido no enunciado (`chamado_id`, `tipo`, `cpf`, `status_anterior`, `status_novo`, `titulo`, `descricao`, `timestamp`).
- [ ] **R-WH-2** — O serviço valida o header `X-Signature-256` em toda requisição de webhook.
- [ ] **R-WH-3** — A validação de assinatura depende de um **secret** configurado via variável de ambiente.
- [ ] **R-WH-4** — A validação usa **HMAC-SHA256** sobre o **body cru** da requisição e comparação em tempo constante.
- [ ] **R-WH-5** — **Idempotência para eventos idênticos:** dois webhooks com payload **idêntico** byte a byte são tratados como o mesmo evento e geram apenas um registro.
- [ ] **R-WH-6** — **Eventos não idênticos:** se a mesma transição chegar com `timestamp` diferente ou outro campo divergente, o sistema persiste ambos os eventos.
- [ ] **R-WH-7** — Requisição sem assinatura ou com assinatura inválida responde `401 Unauthorized`.
- [ ] **R-WH-8** — Payload inválido responde `400 Bad Request`.
- [ ] **R-WH-9** — O campo `tipo` aceita apenas `status_change`; qualquer outro valor responde `400 Bad Request`.
- [ ] **R-WH-10** — O campo `cpf` deve conter 11 dígitos numéricos; valor fora desse formato responde `400 Bad Request`.
- [ ] **R-WH-11** — O campo `timestamp` deve ser parseável como RFC3339; valor inválido responde `400 Bad Request`.
- [ ] **R-WH-12** — O processamento do webhook é **síncrono até a persistência**.
- [ ] **R-WH-13** — O broadcast WebSocket ocorre **após** o commit da transação.
- [ ] **R-WH-14** — Falha no broadcast em tempo real **não** invalida um webhook já persistido; nesse caso, respondemos `200 OK`, registramos erro e seguimos.
- [ ] **R-WH-15** — Evento duplicado não deve ser republicado no Redis.

### Autenticação (REST + WebSocket)

- [ ] **R-AUTH-1** — Toda requisição à API REST e toda conexão WebSocket é autenticada via **JWT Bearer token**.
- [ ] **R-AUTH-2** — O CPF do cidadão é extraído do claim `preferred_username` do JWT.
- [ ] **R-AUTH-3** — Requisições ou conexões sem token, com token malformado, expirado, assinatura inválida ou sem `preferred_username` respondem `401`.
- [ ] **R-AUTH-4** — O algoritmo adotado no MVP é **RS256**, com chave pública obtida via **JWKS endpoint** configurado por ambiente (`JWT_JWKS_URL`).
- [ ] **R-AUTH-5** — A mesma lógica de autenticação é aplicada ao handshake do WebSocket (`GET /ws`).
- [ ] **R-AUTH-6** — Claims mínimas validadas: `exp` e `preferred_username`.
- [ ] **R-AUTH-7** — Em desenvolvimento local, o ambiente sobe um **mock de IdP** que expõe um JWKS de teste e permite emitir JWTs válidos para uso manual e em testes integrados.

### WebSocket

- [ ] **R-WS-1** — O serviço expõe o endpoint `WS /ws`.
- [ ] **R-WS-2** — Quando um webhook é processado com sucesso, o cidadão correspondente **se estiver conectado** recebe a notificação em tempo real pelo WS.
- [ ] **R-WS-3** — O formato da mensagem enviada pelo WS é JSON **idêntico ao recurso da API REST**.
- [ ] **R-WS-4** — O mesmo cidadão pode manter **múltiplas conexões simultâneas**; quando conectado, a notificação é entregue a **todas** as conexões ativas.
- [ ] **R-WS-5** — Heartbeat (ping/pong) periódico existe para detectar conexões mortas e limpar o broadcaster local.
- [ ] **R-WS-6** — Ao fechar ou cair a conexão, ela é removida do broadcaster.
- [ ] **R-WS-7** — O acoplamento webhook → broadcaster ocorre via **Redis pub/sub**.
- [ ] **R-WS-8** — O WebSocket é **best-effort**; se o cidadão estiver desconectado ou o broadcast falhar, a notificação continua disponível via REST.
- [ ] **R-WS-9** — Política de limite de conexões simultâneas e rate limit para WebSocket **não está definida** neste documento e permanece em aberto.

### Privacidade

- [ ] **R-PRIV-1** — CPF **não é persistido em claro** no banco.
- [ ] **R-PRIV-2** — O CPF é substituído por `cidadao_ref = HMAC-SHA256(CPF, CPF_KEY)`.
- [ ] **R-PRIV-3** — `CPF_KEY` é um secret interno configurado via variável de ambiente e **distinto** de `WEBHOOK_SECRET` e da configuração de autenticação JWT (`JWT_JWKS_URL`).
- [ ] **R-PRIV-4** — O CPF em claro só existe em memória durante autenticação e processamento da requisição.

### API REST

> Isolamento por CPF aplica-se a todos os endpoints — ver **R-ISO-3**.

**`GET /notifications`**

- [ ] **R-API-1** — Retorna notificações do cidadão autenticado.
- [ ] **R-API-1.1** — Paginação por **limit/offset**.
- [ ] **R-API-1.2** — Defaults: `limit = 20`, `offset = 0`.
- [ ] **R-API-1.3** — Limite máximo: `limit <= 100`.
- [ ] **R-API-1.4** — Ordenação por `event_timestamp DESC`, com `id DESC` como desempate.
- [ ] **R-API-1.5** — Resposta no formato `{ "items": [...], "total": N, "limit": L, "offset": O }`.

**`PATCH /notifications/:id/read`**

- [ ] **R-API-2** — Marca a notificação como lida apenas se pertencer ao cidadão autenticado.
- [ ] **R-API-2.1** — A operação é **idempotente**: marcar como lida uma notificação já lida retorna sucesso.
- [ ] **R-API-2.2** — Notificação inexistente ou pertencente a outro cidadão responde `404`.
- [ ] **R-API-2.3** — Retorna **`200 OK`** com o recurso atualizado no body.

**`GET /notifications/unread-count`**

- [ ] **R-API-3** — Retorna o total de notificações **não lidas** do cidadão autenticado.
- [ ] **R-API-3.1** — Formato da resposta: `{ "unread_count": N }`.

### Stack

- [ ] **R-STK-1** — Serviço em Go 1.24+ usando Gin.
- [ ] **R-STK-2** — PostgreSQL com queries SQL diretas, sem ORM.
- [ ] **R-STK-3** — Redis é usado para pub/sub do broadcaster.
- [ ] **R-STK-4** — `docker compose up` deve subir o sistema do zero.
- [ ] **R-STK-5** — `just` é o task runner.
- [ ] **R-STK-6** — O `justfile` deve conter pelo menos `up`, `down`, `run` e `test`.

### Qualidade de código

- [ ] **R-COD-1** — Código Go idiomático.
- [ ] **R-COD-2** — Separação de responsabilidades entre transporte, lógica e persistência.
- [ ] **R-COD-3** — Tratamento de erro consistente.

### Testes

- [ ] **R-TST-1** — `just test` executa a suíte e passa.
- [ ] **R-TST-2** — Testes priorizam **dependências reais** (Postgres e Redis); mocks apenas onde a dependência for realmente externa ao projeto.
- [ ] **R-TST-3** — Cobertura de testes **≥ 90%**.
- [ ] **R-TST-4** — Existe teste de validação de assinatura do webhook.
- [ ] **R-TST-5** — Existe teste de rejeição de payload inválido.
- [ ] **R-TST-6** — Existe teste de idempotência do webhook.
- [ ] **R-TST-7** — Existe teste de isolamento por cidadão no `GET /notifications`.
- [ ] **R-TST-8** — Existe teste de `PATCH /notifications/:id/read` para notificação do próprio cidadão.
- [ ] **R-TST-9** — Existe teste de `PATCH /notifications/:id/read` retornando `404` para notificação de outro cidadão.
- [ ] **R-TST-10** — Existe teste de `GET /notifications/unread-count`.
- [ ] **R-TST-11** — Existe teste de entrega WebSocket para cidadão conectado.
- [ ] **R-TST-12** — Existe teste garantindo que cidadão A não recebe notificação de cidadão B.

### Entregáveis

- [ ] **R-ENT-1** — Repositório Git **público**.
- [ ] **R-ENT-2** — Histórico de commits mostrando a evolução do trabalho (sem squash final em único commit).
- [ ] **R-ENT-3** — README na raiz cobrindo: (a) como rodar, (b) **registro de decisões** tomadas e justificativas, (c) o que faria diferente com mais tempo.
- [ ] **R-ENT-4** — Projeto compreensível em "cold start": qualquer avaliador consegue subir e entender sem precisar perguntar nada.

### Bônus / Diferenciais (opcionais)

- [ ] **R-BON-1** — Testes de carga com k6.
- [ ] **R-BON-2** — Dead letter queue para webhooks que falharam na persistência.
- [ ] **R-BON-3** — Circuit breaker para dependências externas.
- [ ] **R-BON-4** — Tracing com OpenTelemetry.
- [ ] **R-BON-5** — Manifests Kubernetes.

---

## O que ainda falta definir

### Decisões em aberto para o MVP

- [ ] Política de limite de conexões simultâneas no WebSocket.
- [ ] Política de rate limit para conexões WebSocket.
- [ ] Política de rate limit para endpoints HTTP.

### Decisões explicitamente fora do MVP

- [ ] Refresh token.
- [ ] Tuning fino de pool, timeouts e throughput.
- [ ] Meta rígida de cobertura como critério de aceite.

### Dúvida remanescente de produto

- [ ] Se o sistema-fonte reenviar um evento semanticamente igual com body diferente, o MVP persiste ambos. Se o produto exigir outra semântica no futuro, a estratégia de idempotência terá de mudar junto com o contrato do emissor.

---

## Especificação de implementação

Os requisitos acima cobrem **o quê** o sistema faz; esta seção lista **o como** para orientar os agentes.

### Estrutura e organização

- [ ] **I-STR-1** — Layout sugerido:
  ```
  cmd/api/main.go
  internal/config/
  internal/auth/
  internal/webhook/
  internal/notification/
  internal/broadcast/
  internal/storage/
  migrations/
  ```

- [ ] **I-STR-2** — Camadas:
  - **Transport:** handlers HTTP e WS.
  - **Service:** regras de negócio e fluxo transacional.
  - **Repository:** acesso SQL ao banco.
  - **Broadcast:** publisher/subscriber Redis.

- [ ] **I-STR-3** — Sem `pkg/` no MVP; o código da aplicação fica sob `internal/` para deixar clara a barreira de import.

- [ ] **I-STR-4** — Separação mínima esperada por responsabilidade:
  - `internal/webhook/`: handler do webhook e validação de assinatura;
  - `internal/notification/`: domínio de notificações, handlers REST e WS, service;
  - `internal/auth/`: middleware JWT e cliente JWKS;
  - `internal/storage/`: repositórios e queries SQL;
  - `internal/broadcast/`: publisher/subscriber Redis;
  - `internal/config/`: carregamento e validação de configuração.

### Bibliotecas e versões

- [ ] **I-LIB-1** — JWT: usar uma biblioteca madura e mantida para validação de JWT com suporte a RS256 e JWKS, preferencialmente `golang-jwt/jwt/v5`.
- [ ] **I-LIB-2** — WebSocket: usar uma biblioteca consolidada; `gorilla/websocket` é aceitável para o MVP.
- [ ] **I-LIB-3** — PostgreSQL: usar `pgx/v5` direto ou `pgxpool`.
- [ ] **I-LIB-4** — Redis: usar cliente compatível com pub/sub e contexto, como `go-redis`.
- [ ] **I-LIB-5** — Logger: usar logger estruturado; `log/slog` é suficiente para o MVP.
- [ ] **I-LIB-6** — Migrations: usar ferramenta versionada e reproduzível, como `golang-migrate` ou `goose`.

### Persistência e fluxo do webhook

- [ ] **I-PER-1** — Fluxo do webhook:
  - validar assinatura;
  - validar payload;
  - calcular `cidadao_ref`;
  - calcular `event_hash`;
  - inserir no banco;
  - se duplicado, responder `200`;
  - se inserido, commitar;
  - publicar no Redis;
  - responder `200`.

- [ ] **I-PER-2** — Duplicata não republica evento.

- [ ] **I-PER-3** — O commit da transação ocorre antes da publicação no Redis.

- [ ] **I-PER-4** — O banco é a fonte de verdade; o broadcast não participa da transação.

### Configuração e segredos

- [ ] **I-CFG-1** — Variáveis mínimas:
  - `WEBHOOK_SECRET`
  - `CPF_KEY`
  - `JWT_JWKS_URL`
  - `DATABASE_URL`
  - `REDIS_ADDR`

- [ ] **I-CFG-2** — O serviço deve falhar ao subir se algum secret obrigatório estiver ausente.
- [ ] **I-CFG-3** — No ambiente local, `JWT_JWKS_URL` aponta para o mock de IdP subido junto da stack.

- [ ] **I-CFG-4** — Deve existir um `.env.example` versionado documentando as variáveis necessárias para desenvolvimento local.

- [ ] **I-CFG-5** — O arquivo `.env` local não deve ser commitado.

- [ ] **I-CFG-6** — A aplicação deve fazer **fail-fast** de configuração na inicialização, validando formato mínimo de `DATABASE_URL`, `REDIS_ADDR` e presença dos secrets obrigatórios.

- [ ] **I-CFG-7** — `WEBHOOK_SECRET`, `CPF_KEY` e a configuração de autenticação JWT têm papéis distintos e não devem ser reutilizados entre si.

### Infraestrutura e runtime

- [ ] **I-INF-1** — O `docker-compose` do ambiente local deve subir pelo menos: aplicação, Postgres, Redis e mock de IdP.

- [ ] **I-INF-2** — O serviço da aplicação deve depender das dependências necessárias estarem saudáveis antes de iniciar o fluxo principal.

- [ ] **I-INF-3** — A aplicação deve encerrar de forma limpa em `SIGTERM`, fechando conexões com Postgres, Redis e recursos internos.

- [ ] **I-INF-4** — A existência de hot reload em desenvolvimento é opcional e não bloqueia o MVP.

### Broadcast

- [ ] **I-BRD-1** — Um canal Redis único é suficiente para o MVP.
- [ ] **I-BRD-2** — O payload publicado contém apenas os dados necessários para reentrega WS.
- [ ] **I-BRD-3** — Cada instância recebe do Redis e entrega apenas para suas conexões locais.

### Testes e organização da suíte

- [ ] **I-TST-1** — Os testes devem ser organizados de forma que unidade e integração sejam distinguíveis de forma clara no repositório e na execução.

- [ ] **I-TST-2** — Os cenários críticos do enunciado devem ser cobertos por testes de integração com dependências reais.

- [ ] **I-TST-3** — Deve existir estratégia explícita para gerar JWTs válidos nos testes, preferencialmente reutilizando o mock de IdP local ou fixtures compatíveis com o JWKS configurado.

- [ ] **I-TST-4** — Seeds e fixtures podem ser feitos por helpers de teste, sem necessidade de arquivos de fixture separados no MVP.

- [ ] **I-TST-5** — A nomenclatura dos testes deve refletir comportamento observado, por exemplo cobrindo webhook, REST, autenticação, isolamento por cidadão e entrega WS.
