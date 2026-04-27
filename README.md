# Serviço de Notificações — Prefeitura do Rio

Serviço de notificações em tempo real para chamados de manutenção urbana. Recebe status via webhook e entrega ao cidadão via REST e WebSocket.

O projeto foi conduzido primeiro pela análise em [`analisa-de-requerimentos.md`](/home/danielpassy/Projects/desafio-prefeitura-rio-backend/analisa-de-requerimentos.md) e depois pelo plano em [`PLAN.md`](/home/danielpassy/Projects/desafio-prefeitura-rio-backend/PLAN.md). O desenvolvimento foi feito com apoio de um agente de IA, usando esses documentos como base para implementação e documentação.

## Setup

Instale o [Docker](https://docs.docker.com/get-started/get-docker/) e o [just](https://just.systems/man/en/packages.html), depois:

```bash
cp .env.example .env
just up       # sobe Postgres, Redis e mock IdP
just migrate  # aplica as migrations
just run      # inicia a aplicação fora do Compose
just test-compose  # roda go test dentro do docker compose
```

`just run` lê as variáveis do ambiente e roda o binário direto na máquina. Se preferir rodar dentro do Compose, use `just up-app` para subir apenas o serviço da aplicação depois de `just up` e `just migrate`.

## Índice de Decisões

1. [Acoplamento via Redis Pub/Sub](#1-acoplamento-via-redis-pubsub): Notificações em tempo real com broadcast eficiente, entrega *best-effort*.
2. [Privacidade por default (citizen_ref)](#5-privacidade-do-cpf-citizen_ref): Mascara o CPF via hash HMAC imediatamente, minimizando tráfego em texto limpo.
3. [Dead Letter Queue com AOF](#3-dead-letter-queue-com-aof): Retentativas via Redis com delay exponencial, sem travar outras mensagens.
4. [Autenticação RS256 + JWKS](#4-autenticação-rs256--jwks): Delega a assinatura ao IdP, mantendo o serviço apenas como verificador.
5. [Mock IdP customizado](#6-mock-idp-customizado): Permite claims dinâmicos essenciais para injetar múltiplos CPFs nos testes.
6. [Migrations separadas](#5-migrations-separadas): Executadas antes do deploy, isolando riscos de banco e código.


## Índice de Melhorias (Com mais tempo)

1. [Repo AI friendly](#1-repo-ai-friendly): Tornar o repositório mais fácil de usar com IA, adicionando docs de padrões de projeto, `agents.md`, `skills`, `CLAUDE.md` e guias parecidos.
2. [Rate limit e cap de conexões](#2-rate-limit-e-cap-de-conexões): Proteção contra abusos no webhook e WebSocket via API Gateway.
3. [Replay da Dead Queue](#3-replay-da-dead-queue): Endpoint ou CLI para re-injetar manualmente mensagens que falharam definitivamente.
4. [Métricas Prometheus](#4-métricas-prometheus): Monitoramento contínuo de webhooks, latência, profundidade da DLQ e WebSockets.
5. [Fault injection nos testes](#5-fault-injection-nos-testes): Simulação de erros no Redis/Postgres para cobrir fluxos de falha reais.
6. [Backpressure no broadcaster](#6-backpressure-no-broadcaster): Drop explícito de mensagens para clientes WS lentos, evitando gargalos globais.
7. [Rotação de CPF_KEY](#7-rotação-de-cpf_key): Estratégia de versionamento para permitir a troca segura do segredo de hash.
8. [Mascaramento de dados](#8-mascaramento-de-dados): Ocultação automática de informações sensíveis em logs e stack traces.
9. [Expansão de testes e DLQ](#9-expansão-de-testes-e-dlq): Cobertura explícita dos fluxos de retry/dead em Redis e revisão do desenho da fila.
10. [Extrair fixtures de autenticação](#10-extrair-fixtures-de-autenticação): Extrair o client/fixture de autenticação compartilhado entre módulos de teste.
11. [Validador mais robusto](#11-validador-mais-robusto): Trocar validações manuais por uma lib Go como `go-playground/validator/v10`.
12. [Property tests](#12-property-tests): Validar invariantes com geração de casos em vez de depender só de exemplos fixos.
13. [Limites e CORS](#13-limites-e-cors): Endurecer a borda HTTP e WebSocket com limites e política de origem.
14. [Imagem Docker mais enxuta](#14-imagem-docker-mais-enxuta): Reduzir superfície de ataque e footprint do runtime.

---

## Kubernetes

Os manifests estão em `k8s/`. **Secrets nunca são commitados** — devem ser criados manualmente antes de aplicar os demais recursos.

```bash
# 1. namespace
kubectl apply -f k8s/namespace.yaml

# 2. config da aplicação
kubectl -n notifications apply -f k8s/app/configmap.yaml

# 3. secrets (ajuste os valores antes de executar)
kubectl -n notifications create secret generic postgres-credentials \
  --from-literal=POSTGRES_USER=app \
  --from-literal=POSTGRES_PASSWORD=<senha> \
  --from-literal=POSTGRES_DB=notifications

kubectl -n notifications create secret generic app-secrets \
  --from-literal=DATABASE_URL=postgres://app:<senha>@postgres:5432/notifications \
  --from-literal=WEBHOOK_SECRET=<secret> \
  --from-literal=CPF_KEY=<chave>

# 4. demais recursos
kubectl apply -f k8s/redis/
kubectl apply -f k8s/postgres/
kubectl apply -f k8s/app/
```

Antes do deploy da aplicação, edite `k8s/app/configmap.yaml` com a URL real do IdP em `JWT_JWKS_URL` e `k8s/app/deployment.yaml` com a imagem correta em `image`.

As migrations devem ser rodadas manualmente (`just migrate`) no ambiente de destino antes de subir o Deployment.

O `ConfigMap` da aplicação carrega `REDIS_ADDR`, `JWT_JWKS_URL` e `OTEL_EXPORTER_OTLP_ENDPOINT`. Os segredos vão em `app-secrets` e incluem `DATABASE_URL`, `WEBHOOK_SECRET` e `CPF_KEY`.

> **Nota:** o StatefulSet de Postgres aqui é apenas para demonstração. Em produção, sugiro usar um db managed (aurora, spanner, etc).


## Decisões Detalhadas

### 1. Acoplamento via Redis Pub/Sub

O webhook persiste a notificação no banco e publica um evento em um canal Redis. Cada instância do serviço assina esse canal e entrega o evento apenas para as conexões WebSocket locais relevantes.

**Essência da decisão:** escalabilidade horizontal + o banco (Postgres) é a fonte de verdade; o WebSocket é apenas um atalho de tempo real.

**Implicações:**
- Entrega via WebSocket é *best-effort* (sem ack, retry ou replay).
- Perdas no pub/sub não causam perda de dados - o cliente recupera via `GET /notifications`.
- O modelo é broadcast: todas as instâncias recebem, cada uma filtra localmente.

**Por que Redis Pub/Sub:**
- Fanout nativo (diferente de filas com consumer group).
- Baixo acoplamento entre instâncias → escala horizontal simples.
- Baixa latência e custo operacional (Redis já faz parte da stack).

**Tradeoff:**
- Não há grantia fortes de entrega (ex: at-least-once), mas isso é aceitável pelo modelo do sistema. E, convenhamos, redis é bastante bom, da pra chegar em 99.5% de uptime facilmente. 

- **Alternativas rejeitadas:** Canais em memória (não escalam), pg pubsub(carga extra no banco, redis faz um papel melhor nesse tipo de caso) e Kafka/Streams (complexidade desnecessária para *fire-and-forget*).

### 2. Privacidade do CPF (citizen_ref)
O CPF é convertido em `HMAC-SHA256(cpf, CPF_KEY)` assim que entra no sistema.

**Princípio:** eliminar o dado sensível do fluxo o mais cedo possível.

**Propriedades:**
- Determinístico → permite correlacionar eventos (se não, webhook ser enviado).
- Não reversível sem a chave → seguro para armazenamento e observabilidade.
- CPF nunca é persistido, logado ou propagado.

**Resultado:**
- Banco, logs e métricas usam apenas `citizen_ref`.
- Privacidade é o comportamento padrão, não uma camada adicional.

**Tradeoff:**
- Segurança depende do sigilo da `CPF_KEY`.
- Com DB + chave, é possível recomputar os CPFs.

### 3. Dead Letter Queue com AOF
Falhas de banco vão para um ZSET Redis (`dlq:webhooks:retry`) com agendamento futuro (backoff exponencial).
* **Por quê:** A versão inicial usava LIST + sleep no worker após falha. Problema: travava a fila inteira - uma mensagem em backoff bloqueava as demais.
Circuit breaker global também não resolve: não distingue falha de infra de mensagem ruim.
Solução: mover o backoff para a entry.
DLQ em ZSET (dlq:webhooks:retry) com score = ready_at_unix_ms.
Falhou → reagenda com now + delay (exponencial por attempts)
Worker só consome score <= now (Lua atômico)
Cada mensagem tem seu próprio timing
Resultado: não bloqueia a fila, isola mensagens ruins e evita thundering herd (100 retries simultâneos).
O Redis usa `appendfsync everysec` (AOF) para durabilidade razoável em outages nesse exemplo simplificado.
Esses dados não são cruciais, poderíamos por `appendfsync always` caso consideremos que o custo extra fosse justificado.s

### 4. Autenticação RS256 + JWKS
O JWT é assinado via criptografia assimétrica pelo IdP. O serviço apenas verifica usando uma chave pública obtida via JWKS.
* **Por quê:** Segurança. O serviço não detém segredos capazes de forjar tokens (problema do HS256). Segue o padrão OIDC Core 1.0.

### 5. Migrations separadas
O binário de migration (`just migrate`) roda de forma independente da inicialização da aplicação.
* **Por quê:** Reduz risco. Permite validar o schema em janela controlada antes do binário novo assumir, facilita rollback.


### 6. Mock IdP customizado
Serviço Go enxuto no ambiente local/de testes.
* **Por quê:** Facilidade de desenvolvimento local. Necessidade de claims dinâmicos (injetar CPF vindo no `client_id` diretamente no `preferred_username`). Testou uma solução pronta mas que não forneciam a flexibilidade.

---

## Melhorias Detalhadas (Com mais tempo)

### 1. Repo AI friendly
* **Objetivo:** Tornar o repositório mais simples de navegar e operar com IA, adicionando documentação de padrões de projeto, `agents.md`, `skills`, `CLAUDE.md` e arquivos parecidos.
* **Conteúdo esperado:** Convenções de arquitetura, fluxos de trabalho, decisões recorrentes, pontos de extensão e exemplos mínimos para acelerar o uso por agentes.
* **Motivação:** Reduzir retrabalho, evitar drift entre o que está implementado e o que um agente inferiria, e deixar o projeto mais autoexplicativo para futuros colaboradores humanos e automatizados.

### 2. Rate limit e cap de conexões
* **Proteção API/WS:** Restringir abusos de `POST /webhook` por emissor e limitar conexões ativas por IP no WebSocket (ex: max 1-2 por IP). A ser aplicado no API Gateway ou Load Balancer.

### 3. Replay da Dead Queue
* **Ferramental:** Criar um endpoint admin ou CLI para listar e re-enfileirar mensagens que esgotaram as retentativas e caíram na lista terminal (`dlq:webhooks:dead`).

### 4. Métricas Prometheus
* **Observabilidade:** Expor `/metrics` para contadores vitais: webhooks recebidos, taxas de erro, profundidade da fila DLQ e quantidade de webSockets simultâneos.

### 5. Fault injection nos testes
* **Resiliência:** Implementar helpers que forçam erros nos pools do Redis/Postgres durante os testes de integração para validar as ramificações de fallback e DLQ.

### 6. Backpressure no broadcaster
* **Prevenção de gargalos:** Adicionar políticas de *drop* (descarte explícito) em canais Go do WebSocket caso o cliente esteja consumindo muito devagar, evitando travar a *goroutine* assinante.

### 7. Rotação de CPF_KEY
* **Segurança:** Implementar versionamento de chaves para o HMAC do CPF. Exige criar estratégia para lidar com logs e históricos antigos quando o segredo for rotacionado.

### 8. Mascaramento de dados
* **Segurança de Operação:** Adicionar middlewares e formatadores no logger estruturado para garantir que dados sensíveis (citizen_ref, cpf, ips) não vazem em stack traces e logs.

### 9. Expansão de testes e DLQ
* **Resiliência e clareza:** Expandir a suíte com cenários mais explícitos da DLQ em Redis, cobrindo `retry` (`ZSET`), `dead` (`LIST`), backoff exponencial e falhas de Postgres/Redis. Em paralelo, revisitar o desenho da DLQ para deixar mais claro o que é fila temporária, o que é terminal e onde cada responsabilidade vive.

### 10. Extrair fixtures de autenticação
* **Extrair o client/fixture de autenticação compartilhado entre módulos.** Hoje ainda existe repetição entre testes de autenticação, webhook e notificação.

### 11. Validador mais robusto
* **Substituir validações manuais por `go-playground/validator/v10` ou lib equivalente, para reduzir boilerplate e centralizar erros de payload.**

### 12. Property tests
* **Introduzir testes baseados em propriedades para assinatura, parsing de payload, idempotência e regras da DLQ. A ideia é validar invariantes, não só exemplos fixos.**

### 13. Limites e CORS
* **Definir limites explícitos e política de CORS para `POST /webhook`, REST e WebSocket, evitando abuso e deixando o comportamento de borda mais previsível.**

### 14. Imagem Docker mais enxuta
* **Trocar a imagem final por uma base mais mínima, preferencialmente distroless ou equivalente, para reduzir superfície de ataque e custo de runtime.**


## Dúvidas / pontos em aberto

- **Eventos duplicados, mas não idênticos:** a mesma transição para o mesmo `chamado_id` chegando com `timestamps` (ou outros campos) diferentes — deduplicar ou persistir ambos? Adotamos por ora persistir ambos (não descartar informação potencialmente legítima), mas o comportamento esperado não está definido no enunciado.
