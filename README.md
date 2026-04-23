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

## Dúvidas / pontos em aberto

- **Eventos duplicados, mas não idênticos:** a mesma transição para o mesmo `chamado_id` chegando com `timestamps` (ou outros campos) diferentes — deduplicar ou persistir ambos? Adotamos por ora persistir ambos (não descartar informação potencialmente legítima), mas o comportamento esperado não está definido no enunciado.
