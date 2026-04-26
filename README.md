# Serviço de Notificações — Prefeitura do Rio

Serviço de notificações em tempo real para chamados de manutenção urbana. Recebe atualizações de status via webhook e as entrega ao cidadão via REST e WebSocket.

## Setup

Instale o [Docker](https://docs.docker.com/get-started/get-docker/) e o [just](https://just.systems/man/en/packages.html), depois:

```bash
cp .env.example .env
just up       # sobe Postgres, Redis e mock IdP
just migrate  # aplica as migrations
just run      # inicia a aplicação
```

## Kubernetes

Os manifests estão em `k8s/`. **Secrets nunca são commitados** — devem ser criados manualmente antes de aplicar os demais recursos.

```bash
# 1. namespace
kubectl apply -f k8s/namespace.yaml

# 2. secrets (ajuste os valores antes de executar)
kubectl -n notifications create secret generic postgres-credentials \
  --from-literal=POSTGRES_USER=app \
  --from-literal=POSTGRES_PASSWORD=<senha> \
  --from-literal=POSTGRES_DB=notifications

kubectl -n notifications create secret generic app-secrets \
  --from-literal=DATABASE_URL=postgres://app:<senha>@postgres:5432/notifications \
  --from-literal=WEBHOOK_SECRET=<secret> \
  --from-literal=CPF_KEY=<chave>

# 3. demais recursos
kubectl apply -f k8s/redis/
kubectl apply -f k8s/postgres/
kubectl apply -f k8s/app/
```

Antes do deploy da aplicação, edite `k8s/app/configmap.yaml` com a URL real do IdP em `JWT_JWKS_URL` e `k8s/app/deployment.yaml` com a imagem correta em `image`.

As migrations devem ser rodadas manualmente (`just migrate`) no ambiente de destino antes de subir o Deployment.

> **Nota:** o StatefulSet de Postgres aqui é apenas para demonstração. Em produção, um managed database (Aurora, Cloud Spanner, Cloud SQL, RDS) é a escolha mais adequada: backup automatizado, failover, point-in-time recovery e patches de segurança sem gestão manual. O mesmo vale para Redis — ElastiCache, Memorystore e similares eliminam a operação do StatefulSet.

## Decisões

### Acoplamento webhook → broadcaster do WebSocket: Redis pub/sub

O handler do webhook publica o evento num canal Redis; cada instância do serviço tem um subscriber que, ao receber, entrega a notificação às conexões WS locais do cidadão-alvo.

**Premissa de negócio que sustenta a decisão:** a notificação **nunca se perde** — ela é persistida no Postgres antes do broadcast. O WebSocket é apenas um atalho de latência; a fonte de verdade é o `GET /notifications`. Perder uma mensagem no canal em tempo real não causa perda de dado: na próxima consulta REST (ou na reconexão), o cliente vê a notificação normalmente. Isso significa que **entrega via WS é best-effort** - não precisamos de garantias `at-least-once`, persistência de fila, replay, nem ack.

Honestamente, Redis é bastante robusto - na prática a taxa de encaminhamento bem-sucedido pelo pub/sub deve ficar acima de 99,5%. A premissa acima não é um escudo para negligenciar a entrega; é o que torna aceitável não ir atrás dos 0,x% restantes com Streams/Kafka.

**Alternativas consideradas:**

- **Canal in-memory (Go channel):** mais simples, mas preso a uma única instância — não escala horizontalmente.
- **Postgres LISTEN/NOTIFY (pgpubsub):** mesma semântica fire-and-forget do Redis, sem adicionar infra. Rejeitado porque Redis já é obrigatório na stack e cada `LISTEN` consome um backend process do Postgres — melhor separar papéis (Postgres = estado, Redis = mensageria efêmera).
- **Redis Streams / Kafka / NATS:** oferecem persistência, consumer groups e replay. Rejeitados porque resolvem problemas que não temos (ver premissa acima). Além disso, o modelo de consumer group (uma mensagem vai para um consumidor) não encaixa com a necessidade de fanout (todas as instâncias precisam receber para alcançar a conexão certa).

**Motivos da escolha:**

- Fanout nativo do pub/sub encaixa com o modelo "broadcast para todas as instâncias, cada uma entrega a quem tiver localmente".
- Custo de código sobre in-memory é mínimo e destrava escala horizontal sem reescrita.
- O enunciado menciona Redis como disponível e deixa o uso a critério.

### Migrations separadas do processo da aplicação

As migrations são aplicadas via `just migrate` (binário `cmd/migrate`), não no startup da aplicação.

**Por quê:** código e schema têm perfis de risco diferentes — código reverte em segundos, schema raramente reverte sem risco de perda de dados. Acoplando os dois no startup, qualquer problema força um diagnóstico simultâneo das duas mudanças. Separando, dá para rodar a migration numa janela controlada, validar que o banco está saudável e só depois fazer o deploy do binário — além de viabilizar o padrão expand/contract necessário em rolling deploys.

**Configuração:** o binário de migration usa o mesmo loader de configuração da aplicação. Isso exige que o ambiente tenha o mesmo conjunto de variáveis/secrets do serviço, mesmo que a migration use diretamente só o banco. A decisão é intencional para manter o MVP simples: no deploy esperado, migration e aplicação rodam no mesmo ambiente operacional e recebem o mesmo bundle de configuração.

**Alternativa considerada:** executar no startup (o goose usa advisory lock, então múltiplas instâncias não conflitam). Rejeitado pelo motivo acima.

### Autenticação: RS256 + JWKS

O JWT do cidadão é assinado com **RS256** (criptografia assimétrica) e a chave pública é obtida via **JWKS endpoint** configurado em `JWT_JWKS_URL`. Nosso serviço só **verifica** assinatura — nunca tem a chave privada.

**Premissa de negócio que sustenta a decisão:** o JWT é emitido por **outro serviço** (o IdP da Prefeitura) — não somos nós que assinamos. O enunciado não nomeia o IdP, mas o uso do claim `preferred_username` é padrão **OIDC Core 1.0**, o que indica fortemente que o emissor é um provedor OIDC (gov.br, Keycloak, Entra e similares). Assumindo esse cenário, o padrão de fato é **RS256 + JWKS**: em produção, basta apontar `JWT_JWKS_URL` para o IdP real — sem mudança de código, sem troca de segredo compartilhado, sem rotação manual de chave.

**Por que assimétrica e não simétrica (HS256):** com HS256 o segredo usado para **assinar** é o mesmo usado para **verificar**. Para validarmos tokens, teríamos que guardar esse segredo — e quem o tem pode forjar JWT em nome de **qualquer cidadão**. O serviço de notificações não tem motivo legítimo para esse poder. Com RS256, o IdP mantém a chave privada (assinatura) e distribui só a pública (verificação); um comprometimento do nosso lado não permite emitir tokens. Além disso, HS256 exigiria um canal seguro de distribuição do segredo entre IdP e cada serviço consumidor — RS256 resolve via JWKS público.

**Alternativas consideradas:**

- **HS256 (HMAC com segredo compartilhado):** mais simples, mas exige que o segredo de assinatura viva também no nosso serviço. Qualquer comprometimento do nosso lado permite forjar tokens em nome de qualquer cidadão. Rejeitado: o serviço de notificações não tem motivo para ter poder de emitir JWT.
- **RS256 com chave pública em variável de ambiente (PEM literal):** funciona, mas força redeploy a cada rotação de chave do IdP e não suporta múltiplas chaves ativas (comum durante rotação). JWKS resolve os dois problemas nativamente.

**Motivos da escolha:**

- Separação de papéis correta: IdP assina, nós só verificamos.
- JWKS é o mecanismo padrão OIDC para distribuição e rotação de chave pública — qualquer IdP sério expõe `/.well-known/jwks.json`.
- Para desenvolvimento, subimos um **mock de IdP no compose** que expõe um JWKS de teste e emite tokens assinados; a aplicação não distingue mock de produção.

### Privacidade do CPF: citizen_ref

O enunciado exige que o CPF não apareça no banco. A solução adotada é substituí-lo por `citizen_ref = HMAC-SHA256(cpf, CPF_KEY)`, onde `CPF_KEY` é um secret distinto do `WEBHOOK_SECRET` e da configuração JWT.

O objetivo além de cumprir o requisito foi **minimizar o trecho do sistema em que o CPF circula em plaintext**. O CPF aparece em dois pontos de entrada:

- **Webhook:** vem no body JSON, o handler computa o `citizen_ref` imediatamente e descarta o CPF — ele nunca é persistido nem repassado adiante.
- **REST/WebSocket:** vem no claim `preferred_username` do JWT; o auth middleware computa o `citizen_ref` e armazena apenas ele no contexto da requisição — os handlers nunca têm acesso ao CPF em si.

Em ambos os casos, o CPF existe apenas em memória durante o processamento de uma única requisição. Quem acessa o banco vê só o hash; quem acessa os handlers vê só o `citizen_ref`. O CPF nunca é logado, propagado entre camadas nem armazenado.

Uma consequência prática dessa abordagem é que o `citizen_ref` se torna **cidadão de primeira classe para desenvolvedores e operadores**: logs, stack traces, métricas e queries de debug expõem apenas o hash. Não há configuração especial de mascaramento necessária — privacidade é o comportamento default, não uma camada adicionada depois.

**Tradeoff:** `citizen_ref` é determinístico — o mesmo CPF sempre gera o mesmo hash com a mesma chave. Isso é intencional (precisamos correlacionar webhook com REST), mas significa que um atacante com acesso ao banco e à `CPF_KEY` consegue recalcular qualquer `citizen_ref`. A proteção depende do sigilo da `CPF_KEY`, não da irreversibilidade matemática do hash.

### Dead Letter Queue: Redis com AOF

Quando o `repo.Insert` falha (banco indisponível, timeout etc.), o payload já validado é enfileirado numa lista Redis (`dlq:webhooks`). Um worker em background drena essa fila e retenta a persistência quando o banco voltar, até `MaxAttempts = 5`. O sender recebe 500 e pode retentar por conta própria — a DLQ é uma rede de segurança para o caso em que o banco volte depois que o sender desistiu.

**Configuração Redis e o tradeoff de durabilidade:** Redis é in-memory por padrão. Para os itens da DLQ sobreviverem a um restart, é necessário habilitar AOF (`appendonly yes`). A escolha de `appendfsync` tem impacto direto:

- `always` — fsync em toda escrita, máxima durabilidade, mas penalidade de I/O significativa. **Incompatível com pub/sub em alta frequência:** o `PUBLISH` passa pelo AOF e fica limitado à velocidade do disco.
- `everysec` — fsync a cada 1 segundo em background; perda máxima de 1 segundo de dados em queda abrupta. Impacto de throughput desprezível (~1-2%).
- `no` — deixa o SO decidir; sem garantia.

**O que foi adotado:** uma única instância Redis com `appendfsync everysec`, aplicado ao servidor inteiro (AOF não é por chave ou estrutura). A perda máxima de 1 segundo de itens de DLQ é aceitável para notificações de chamados municipais — não é informação de natureza bancária onde qualquer perda é inaceitável.

**Melhoria conhecida para produção (K8s):** o ideal são dois Redis separados — um dedicado à DLQ com `appendfsync always` (escritas excepcionais, baixo throughput, alta durabilidade) e outro para pub/sub sem AOF (alta frequência, entrega best-effort por design). No Kubernetes isso se traduz em dois `Deployment` + `Service` distintos, cada um com seu `ConfigMap` de `redis.conf`. A decisão de usar uma instância única é de simplicidade para esta demonstração.

### Testes HTTP: um único router compartilhado

Os testes HTTP do projeto usam o mesmo router compartilhado da aplicação, em vez de montar routers mínimos por arquivo de teste. A decisão é intencional: o projeto é pequeno, então aceitamos um setup de teste um pouco mais pesado em troca de reduzir duplicação e evitar drift entre o wiring real e o wiring de teste.

**Tradeoff aceito:** alguns testes precisam de fixtures extras que não seriam estritamente necessárias em um teste mais isolado, porque o bootstrap do router real exige dependências adicionais. Ainda assim, por enquanto isso simplifica o repositório como um todo: o setup fica unificado, menos verboso e menos sujeito a ficar desatualizado quando a montagem do router mudar.

**Motivo principal da escolha:** usando o router real, os testes pegam mudanças de comportamento na composição HTTP da aplicação — por exemplo, rota removida, método trocado, middleware esquecido ou endpoint movido de grupo. Com routers mínimos duplicados, esse tipo de regressão pode passar despercebido.

**Quando revisitar:** se desempenho de testes ou custo de setup se tornar um problema real, podemos reintroduzir montagem minimalista por teste nos pontos em que isso trouxer ganho claro. Enquanto isso não acontecer, preferimos manter um único router como fonte de verdade.

## Dúvidas / pontos em aberto

- **Eventos duplicados, mas não idênticos:** a mesma transição para o mesmo `chamado_id` chegando com `timestamps` (ou outros campos) diferentes — deduplicar ou persistir ambos? Adotamos por ora persistir ambos (não descartar informação potencialmente legítima), mas o comportamento esperado não está definido no enunciado.
