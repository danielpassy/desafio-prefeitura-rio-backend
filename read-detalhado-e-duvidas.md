# Desafio Técnico — Análise Detalhada

_Trabalho em progresso — construído tópico a tópico._

## Contexto

O enunciado envolve **dois sistemas distintos**:

1. **Sistema-fonte (externo, fora do escopo):** sistema da Prefeitura que controla o ciclo de vida do chamado (`aberto → em_analise → em_execucao → concluido`) e emite os webhooks de mudança de status.
2. **Serviço de notificações (o que vamos construir):** consome os webhooks, persiste e entrega ao cidadão via WebSocket (tempo real) e REST (sob demanda).

## Webhook

Endpoint `POST` exposto pelo nosso serviço que recebe do sistema-fonte um evento de mudança de status de chamado (JSON com `chamado_id`, `cpf`, `status_anterior`, `status_novo`, etc.). Cada requisição vem com header `X-Signature-256` contendo `sha256=<HMAC-SHA256(body, secret)>` — validamos antes de processar; inválido ou ausente → rejeita. Mesmo evento pode chegar duas vezes (retry do sistema-fonte), e o processamento é **idempotente** (não duplica registro). Após persistir, o evento é propagado para o WebSocket do cidadão correspondente.

## API REST

...

## WebSocket

...

## Privacidade

...

## Stack / Entregáveis / Diferenciais

...

---

## Requisitos

### Modelo de Dados

- [ ] **R-DM-1** — Existe uma tabela `notifications` com a seguinte estrutura:

| Coluna | Tipo | Origem / observação |
|---|---|---|
| `id` | UUID (PK) | gerado por nós |
| `chamado_id` | TEXT | do payload (`CH-2024-001234`) |
| `tipo` | TEXT | do payload (`status_change`) |
| `cidadao_ref` | BYTEA / TEXT | derivado do CPF — não guardamos CPF em claro (ver Privacidade) |
| `status_anterior` | ENUM(`aberto`,`em_analise`,`em_execucao`,`concluido`) | do payload |
| `status_novo` | mesmo ENUM | do payload |
| `titulo` | TEXT | do payload |
| `descricao` | TEXT | do payload |
| `event_timestamp` | TIMESTAMPTZ | do payload (`timestamp`) |
| `received_at` | TIMESTAMPTZ DEFAULT NOW() | nosso (quando chegou o webhook) |
| `lida` | BOOLEAN DEFAULT FALSE | nosso (marcada pelo PATCH) |
| `lida_at` | TIMESTAMPTZ NULL | nosso (quando foi marcada) |

> Detalhes ainda em aberto (decididos em seções seguintes): forma de derivar `cidadao_ref` do CPF, chave de idempotência, índices, e ENUM vs. TEXT+CHECK.

### Isolamento por cidadão (global)

- [ ] **R-ISO-1** — Cada notificação pertence a **exatamente um cidadão**, identificado pelo CPF recebido no payload do webhook. Essa vinculação é a fonte de verdade para todo o resto do sistema.
- [ ] **R-ISO-2** — **Webhook:** ao persistir, a notificação é gravada associada ao CPF do payload; nenhum evento pode "vazar" para outro cidadão.
- [ ] **R-ISO-3** — **REST:** toda query filtra pelo CPF extraído do JWT; recurso de outro cidadão responde `404` (não `403`, para não vazar existência).
- [ ] **R-ISO-4** — **WebSocket:** o cidadão conectado recebe **apenas** notificações vinculadas ao próprio CPF; o roteamento do broadcaster usa o CPF da conexão autenticada.

### Webhook

- [ ] **R-WH-0** — O serviço reconhece os quatro estados possíveis de um chamado: `aberto`, `em_analise`, `em_execucao`, `concluido` (usados nos campos `status_anterior` e `status_novo` do payload).
- [ ] **R-WH-1** — O serviço é capaz de receber requisições `POST` de webhook com os diferentes status de chamado, no formato de payload definido no enunciado (`chamado_id`, `tipo`, `cpf`, `status_anterior`, `status_novo`, `titulo`, `descricao`, `timestamp`).
- [ ] **R-WH-2** — O serviço valida o header `X-Signature-256` em toda requisição de webhook. Requisições **sem** assinatura ou com assinatura **inválida** são rejeitadas.
- [ ] **R-WH-3** — A validação de assinatura depende de um **secret** configurado via variável de ambiente (não hard-coded, não commitado).
- [ ] **R-WH-4** — A validação usa o mecanismo **HMAC-SHA256** sobre o **body cru** da requisição, comparado em **tempo constante** (`hmac.Equal`) contra o valor do header (após remover o prefixo `sha256=`).
- [ ] **R-WH-5** — **Idempotência para eventos idênticos:** dois webhooks com payload **idêntico** (bit a bit) são tratados como o mesmo evento e geram apenas um registro.
- [ ] **R-WH-5.1** — **Eventos quase idênticos (decisão explícita de design):** se a mesma transição chegar com `timestamps` diferentes (ou qualquer outra divergência de campo), são tratadas como **eventos distintos** e **ambas persistidas**. O enunciado não define o comportamento esperado; optamos por não deduplicar para não descartar informação potencialmente legítima do sistema-fonte. Esta escolha fica registrada para o leitor da avaliação.
- [ ] **R-WH-5.1** — **Estratégia de idempotência a definir.** Decisão em aberto — opções discutidas até agora:
  - **(A)** UNIQUE sobre uma tupla de campos do payload (ex.: `chamado_id` + `status_anterior` + `status_novo` + `event_timestamp`).
  - **(B)** UNIQUE sobre `event_hash` = SHA256 do body recebido — deduplica eventos idênticos byte a byte.
  - Tensão conhecida: "falha" é arbitrário (pode ser conexão morta após gravar com sucesso); hash idêntico não distingue "retry real" de "sistema-fonte reemitindo evento legítimo idêntico". Precisa decisão específica depois.

### Autenticação (REST + WebSocket)

- [ ] **R-AUTH-1** — Toda requisição à API REST e toda conexão WebSocket é autenticada via **JWT Bearer token** enviado pelo app do cidadão.
- [ ] **R-AUTH-2** — O **CPF** do cidadão é extraído do claim `preferred_username` do JWT.
- [ ] **R-AUTH-3** — Requisições/conexões **sem** token, com token **malformado**, **expirado** ou com **assinatura inválida** são rejeitadas (`401`).
- [ ] **R-AUTH-4** — A chave/secret de verificação do JWT é configurada via **variável de ambiente** (não hard-coded, não commitado).
- [ ] **R-AUTH-5** — A mesma lógica de autenticação é aplicada ao **handshake do WebSocket** (`GET /ws`): o JWT é validado antes do `Upgrade`; conexões não autenticadas são recusadas.

### WebSocket

- [ ] **R-WS-1** — O serviço expõe o endpoint `WS /ws`.
- [ ] **R-WS-2** — Quando um webhook é processado com sucesso, o cidadão correspondente **se estiver conectado** recebe a notificação em tempo real pelo WS, sem polling.
- [ ] **R-WS-3** — O formato da mensagem enviada pelo WS é JSON (shape a definir — provavelmente alinhado com o recurso da API REST).
- [ ] **R-WS-4** — O mesmo cidadão pode manter **múltiplas conexões simultâneas** (ex.: web + mobile); quando conectado, a notificação é entregue a **todas** as conexões ativas.
- [ ] **R-WS-4.1** — **Controle de abuso de conexões — a definir.** Um limite rígido por CPF evita flood de um mesmo usuário, mas permite que um atacante lock-out um CPF legítimo abrindo conexões até o teto. Já um limite global/por IP protege contra DDoS mas tem suas próprias armadilhas. Trade-off não trivial; decisão fica em aberto para discussão específica.
- [ ] **R-WS-5** — Heartbeat (ping/pong) periódico para manter a conexão viva e detectar clientes mortos.
- [ ] **R-WS-6** — Desconexão limpa: ao fechar ou cair a conexão, ela é removida do broadcaster.
- [ ] **R-WS-7** — **Decisão arquitetural destacada pelo enunciado:** como o receptor do webhook se conecta ao broadcaster do WS. Opções: canal in-memory (simples, não escala horizontalmente) vs. Redis pub/sub (usa o Redis da stack, escala para N instâncias). Decisão a ser tratada em seção própria.

### Privacidade

- [ ] **R-PRIV-1** — CPF **não é persistido em claro** no banco. Alguém com acesso direto ao banco não consegue identificar a qual cidadão os dados pertencem.
- [ ] **R-PRIV-2** — O CPF é substituído por `cidadao_ref = HMAC-SHA256(CPF, CPF_KEY)`. Determinístico (permite filtro por igualdade), uma via (não reversível sem a chave).
- [ ] **R-PRIV-3** — `CPF_KEY` é um secret **interno**, configurado via variável de ambiente, **distinto** do `WEBHOOK_SECRET`.

### API REST

> Isolamento por CPF aplica-se a todos os endpoints — ver **R-ISO-3**.

**`GET /notifications`**

- [ ] **R-API-1** — Retorna notificações do cidadão autenticado (filtradas por CPF).
- [ ] **R-API-1.1** — Paginação por **limit/offset** simples (ex.: `?limit=20&offset=0`), com defaults e **limite máximo** (ex.: `limit` ≤ 100).
- [ ] **R-API-1.2** — Ordenação por `timestamp` **DESC** (mais recentes primeiro).
- [ ] **R-API-1.3** — Resposta inclui os dados da notificação + flag `lida` (bool) + indicação de paginação (total/next) para o cliente saber se há próxima página.

**`PATCH /notifications/:id/read`**

- [ ] **R-API-2** — Marca a notificação como lida, apenas se pertencer ao cidadão autenticado.
- [ ] **R-API-2.1** — **Idempotente:** marcar como lida uma notificação já lida retorna sucesso (não erro).
- [ ] **R-API-2.2** — Notificação inexistente **ou** pertencente a outro cidadão → `404`.
- [ ] **R-API-2.3** — Retorna **`200`** com o recurso atualizado no body (mais conveniente para o cliente evitar refetch).

**`GET /notifications/unread-count`**

- [ ] **R-API-3** — Retorna o total de notificações **não lidas** do cidadão autenticado (filtrado por CPF).
- [ ] **R-API-3.1** — Formato de resposta simples (ex.: `{ "unread_count": 42 }`).

### Stack

- [ ] **R-STK-1** — Serviço em Go 1.24+ usando Gin como framework HTTP.
- [ ] **R-STK-2** — PostgreSQL com queries SQL diretas, sem ORM (`database/sql` + driver `pgx` ou similar).
- [ ] **R-STK-3** — Redis disponível na stack (uso justificado por decisão nossa — provavelmente pub/sub para o broadcaster, ver `R-WS-7`).
- [ ] **R-STK-4** — `docker compose up` sobe o sistema inteiro do zero, **funcionando** (não basta existir — tem que rodar sem passos manuais adicionais).
- [ ] **R-STK-5** — `just` como task runner; comandos principais (`just test`, `just run`, etc.) documentados.

### Qualidade de código

- [ ] **R-COD-1** — Código Go idiomático: convenções da linguagem, nomes claros, organização padrão (`cmd/`, `internal/`, etc.).
- [ ] **R-COD-2** — Separação de responsabilidades entre camadas (transporte, lógica, persistência).
- [ ] **R-COD-3** — Tratamento de erro consistente — política única para propagação, wrapping e resposta HTTP.

### Testes

- [ ] **R-TST-1** — `just test` executa a suíte e passa.
- [ ] **R-TST-2** — Testes usam **dependências reais** (Postgres e Redis) via testcontainers ou compose de teste. Mocks só onde a dependência é genuinamente externa e não controlável.
- [ ] **R-TST-3** — Cobertura de testes **≥ 90%**.

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
