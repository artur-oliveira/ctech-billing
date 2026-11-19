# Web Push: lembrete diário de contas pendentes de validação (Billing)

**Status:** proposta, aguardando aprovação · **Data:** 2026-11-18
**Desenho compartilhado:** `ctech-poker/docs/specs/2026-11-18-scheduled-web-push.md` (pacote `go-common/pushsvc`, tick de 15 minutos no EventBridge Scheduler, par VAPID por serviço, horário em BRT). Esta spec só descreve o que é do Billing.

## O que é "pendente de validação"

Interpretação adotada: **ocorrências de recorrência em `pending_confirmation`**, as que esperam o usuário confirmar o valor
(`RecurrenceRepository.ListPendingOccurrences`). **A confirmar com o dono do produto:** linhas pendentes de importação de
fatura de cartão (`card-statement-import`) também entram?

## Comportamento

- Preferência por usuário: `{"pending_reminder":{"enabled":bool,"time":"HH:MM"}}` em `GET/PUT /v1.0/push/preferences`.
  Começa **desligada** até o usuário escolher um horário.
- No tick, para cada usuário na faixa: lista os espaços dele, soma as ocorrências pendentes e envia **só se a soma for maior que
  zero**: "Você tem N contas aguardando confirmação." Sem valores nem nomes de contas no payload (a tela de bloqueio mostra).
  Link para a tela de pendências.
- No máximo 1 envio por dia (`last_sent_day` com condição). Espaços compartilhados: cada membro recebe o seu, pelas suas
  preferências.

## Infra (Terraform, não CDK)

- Tabela `billing_push` em `terraform/billing/dynamodb.tf`, com a criptografia gerenciada **explicitamente desativada**
  (`enabled = false`, política de custo ocioso zero), TTL, GSI esparso `slot-index`.
- `aws_scheduler_schedule` `cron(0/15 * * * ? *)` em `America/Sao_Paulo`, copiando `byok_warm.tf`.
- Lambdas `push-tick` e `push-sender` (`cmd/push-tick`; `push-sender` vem do `pushsvc`), role mínima, parâmetros VAPID em SSM.
- Contas e usuários do Billing continuam no domínio dele; o `pushsvc` guarda só inscrições.

## Custo

~US$ 0,2/mês com 10 mil usuários, 30% ativos (checagem de pendências por espaço é 1 query; envio só quando há pendência).

## Fases

F1 `pushsvc` (do spec compartilhado) → F2 API/tick/Terraform → F3 UI (tela de notificações no console) → F4 liga as chaves e
confere o job de Deploy.
