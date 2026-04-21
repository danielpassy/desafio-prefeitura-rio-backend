# Desafio Técnico — Back-end Pleno (Golang)

## Contexto

Cidadãos do Rio abrem chamados de manutenção urbana por um sistema público da Prefeitura para sanar problemas como buraco na rua, iluminação com defeito, lixo acumulado. Cada chamado percorre um ciclo: aberto, em análise, em execução, concluído. O problema é que o cidadão abre o chamado e muitas vezes nunca mais sabe o que aconteceu.

Sua tarefa é construir o serviço de notificações: recebe atualizações de status desse sistema e as entrega ao cidadão em tempo real.

## O que construir

### Webhook

O sistema envia um `POST` para o seu serviço toda vez que o status de um chamado muda:

```json
{
  "chamado_id": "CH-2024-001234",
  "tipo": "status_change",
  "cpf": "12345678901",
  "status_anterior": "em_analise",
  "status_novo": "em_execucao",
  "titulo": "Buraco na Rua — Atualização",
  "descricao": "Equipe designada para reparo na Rua das Laranjeiras, 100",
  "timestamp": "2024-11-15T14:30:00Z"
}
```

O payload vem assinado: o header `X-Signature-256` contém `sha256=<HMAC-SHA256 do body com o secret>`. O secret é configurado via variável de ambiente. Requisições sem assinatura ou com assinatura inválida devem ser rejeitadas.

O sistema pode re-enviar o mesmo evento em caso de falha. Receber o mesmo evento duas vezes não pode criar dois registros.

### API REST

O app do carioca envia um JWT como Bearer token — o CPF do cidadão está no campo `preferred_username`. Os endpoints necessários:

- `GET /notifications` — lista as notificações do cidadão autenticado, com paginação
- `PATCH /notifications/:id/read` — marca uma notificação como lida
- `GET /notifications/unread-count` — retorna o total de não lidas

Um cidadão não acessa notificações de outro. Os paths acima são sugestão — o design final é decisão sua.

### WebSocket

O app mantém uma conexão `WS /ws` para receber notificações sem precisar fazer polling. Quando um evento chega e é processado, o cidadão conectado deve receber a notificação. Como você conecta o receptor do webhook ao broadcaster de WebSocket é uma das decisões que queremos ver.

### Privacidade

CPF não pode aparecer em texto no banco. Se alguém acessar o banco diretamente, não deve conseguir identificar a qual cidadão os dados pertencem.

## Stack

- Go 1.24+ com Gin
- PostgreSQL para persistência, com queries SQL diretas (sem ORM)
- Redis disponível na stack — use como achar adequado
- Docker — `docker compose up` tem que subir tudo do zero
- `just` como task runner

## O que entregar

Repositório Git público com histórico de commits que mostre como o trabalho evoluiu. O README deve explicar como rodar o projeto, as decisões que você tomou e o que faria diferente com mais tempo. `just test` deve passar.

## O que olhamos

Funcionalidade primeiro: webhook, REST e WebSocket se comportam como especificado? As regras de segurança e privacidade foram respeitadas? Depois, código: é Go idiomático, a separação de responsabilidades faz sentido, o tratamento de erro é consistente? Testes que usam dependências reais pesam mais do que testes com mocks. E por fim, conseguimos subir e entender o projeto sem precisar perguntar nada?

As decisões de design como schema, estrutura de pastas, nomes de endpoint — não têm resposta certa. Têm decisões bem ou mal justificadas.

## Diferenciais

- Testes de carga com k6
- Dead letter queue para webhooks que falharam na persistência
- Circuit breaker para dependências externas
- Tracing com OpenTelemetry
- Manifests Kubernetes

---

Dúvidas: **selecao.pcrj@gmail.com**
