# Escala, performance e custo — diagnóstico e plano (2026-10-18)

Origem: quatro auditorias de leitura (dados, API, motor de cobrança, infra/frontend). **Nada foi medido nem
rodado**: tempos e custos são estimativas de leitura de código. Os itens marcados ✔ foram reconferidos por mim
no código; os demais vêm do relatório do agente e precisam ser reproduzidos antes de virar PR.

Restrições do dono já fixadas: mais simples/barato em escala, **sem alarmes CloudWatch** nem métricas pagas,
API em on-demand + EC2 Instance Savings Plan (spot descartado), e **toda melhoria de infra reutilizável vai
para o repositório compartilhado** (ctech-go-common / ctech-cdk / ctech-ui), não só para o billing.

## 1. O que quebra, e onde

| Escala | O que quebra primeiro |
|---|---|
| 1–1k usuários | Nada de volume. Mas já há **perda silenciosa de receita/lembrete** (P0). |
| 10k | Job financeiro atrasa; nano (512 MB, CPU burstable) estrangula; extrato de contas grandes. |
| 100k | Backlog dos jobs cresce sem teto; partições de índice de agenda quentes; webhooks FIFO globais (≈50/min). |
| Entidade com >100k linhas | Extrato, fluxo de caixa, gastos por cartão, lista de compras: leitura sem limite, ~25–30 MB por chamada, OOM no nano com 2–3 chamadas simultâneas. Purge/export estouram o visibility timeout e a memória. |
| Merchant com 100k assinaturas no mesmo dia | Sweep serial (≈3 h por 100k; 14–28 h com cobrança BYOK inline), webhooks ≈67 h de fila, Inter 429 em tempestade, contador de numeração como ponto único. |

## 2. P0 — corrigir antes de escalar (perdem dinheiro hoje, esforço S)

1. **Dunning lê uma página de 200 e para** ✔ (`services/dunning.go:81`, `DueForDunning(..., 200, nil)`). Acima de 200
   faturas vencendo no dia, o resto nunca recebe lembrete, escalada nem abandono. Fix: seguir `LastEvaluatedKey`
   + pool de workers + conjunto de "já vistos" contra linha venenosa.
2. **Sweep só lê a partição da data de hoje** ✔ (`DueOn(date)`, exato). Se uma execução falha ou estoura 24 h,
   o dia seguinte não recupera o anterior. Fix: consultar todas as datas `<= hoje` (ou watermark).
3. **Período não faturado se o finalize falhar** (leitura: `GenerateForPeriod` cria o DRAFT e depois finaliza;
   num rerun, `ErrAlreadyGenerated` segue direto para a renovação e o DRAFT fica sem emitir). Fix: ao receber
   `ErrAlreadyGenerated`, carregar a fatura pela chave de geração e finalizar se for DRAFT; mais um varredor
   "DRAFT com mais de 1 h" que registra em log (sem alarme). *Reproduzir com teste antes.*
4. ~~**Cobrança pela wallet sem reconciliação**~~ **Falso positivo, retirado.** A rail da wallet foi removida
   (#200, `6755480`); `Pay()` devolve `ErrByokNotConnected` para quem não é BYOK, e o BYOK tem sweep próprio
   para o que um webhook perdeu. `PendingOn` é código morto, sem risco de receita.
5. **Endpoint de extrato sem limite** ✔ (`EntriesFrom` lê `ENTRY#from` até `ENTRY#~`; `finance_ledger.go:54`).
   Ver P1.

## 3. P1 — entidades de 100k+ linhas (dados)

- **Extrato**: limitar a `[from,to)`, saldo de abertura = saldo da conta − Σ SUMMARY(from..hoje) (conferir que o
  SUMMARY é por conta e mês, como o agente afirma), cursor + limite. S–M, sem migração.
- **`queryPrefix` com `Limit:100` e leitura consistente** (`ledger.go:640`): tirar o `Limit` (leitura de até
  1 MB por página, ~10–40× menos idas e voltas) e usar leitura eventual onde não há guarda de consistência. S.
- **Fluxo de caixa**: hoje O(entradas da janela × contas) e relê todos os REVERSAL# por conta. Fix: linha
  `FLOWSUM#mês#conta#fluxo` incrementada junto do SUMMARY + flag `reversed` na entrada. M, com backfill (o
  comando `finance-rebuild` já percorre as transações).
- **Gastos por cartão / compras**: `ProjectionExpression` + resumo mensal por categoria + paginação. M.
- **Jobs financeiros**: `jobBatch = 5000` sem paginar, serial, GetItem por item. Paginar + pool (~16) +
  shard na chave `schedule_pk` (hash do dono). M, com backfill.
- **Purge/erasure/export** em streaming (página → apaga/escreve), 8–16 workers, heartbeat de visibility,
  NDJSON direto para o zip/S3 multipart; hoje 200–600 MB de pico **dentro do processo da API**. M.
- **Auditoria por entidade** incompleta além de ~1000 linhas: índice por entidade (já citado no ADR 0002). M.
- **GSIs `ALL`**: trocar por `KEYS_ONLY`/`INCLUDE` nos índices de agenda, lookup e período de auditoria
  (o job já relê a linha). Cada escrita indexada hoje grava o item duas vezes. M, reconstrução de índice.

## 4. P2 — compartilhado (ctech-go-common / ctech-cdk), beneficia a família inteira

| Item | Onde | Quem ganha |
|---|---|---|
| Cache em processo (chaves JWKS já parseadas, revogação, bloqueio de conta, credencial M2M, org, "espaço pronto") com singleflight e invalidação via hook | go-common (`jwtverify`, `erasure`, `accountorgs`, `oauth2client`) | todos os serviços Go. Hoje cada request paga 2 GET no Valkey + 2–3 leituras DynamoDB; se o Valkey piscar, cada request bate no JWKS da account (manada). |
| `http.Transport` ajustado (MaxIdleConnsPerHost 64, HTTP/2) + circuit breaker + cache negativo | go-common | todos; hoje >2 chamadas simultâneas à account redisca TLS |
| Middleware de deadline por handler (Fiber v3 `c.Context()` não cancela) | go-common | todos os serviços Fiber |
| Rate limit por tenant/espaço (contador no Valkey) | go-common | billing, wallet, dfe |
| `GOMEMLIMIT` (~50% da caixa) no user-data do ASG | ctech-cdk (poker já tem, billing não) | todos |
| Access log do nginx só com status ≥400/lentos para o CloudWatch (completo só em S3) e retenção 7 dias | ctech-cdk `setup-nginx.sh` | todos; estimativa ~US$8–15/mês a 100k usuários |
| `client_max_body_size` por rota (import de extrato em base64 passa de 1 MB a ~750 KB → 413 opaco) | ctech-cdk (parâmetro) + billing | billing; *valor 1m não localizei no terraform, só no relatório* |
| Loop do `deliver` em processo (goroutine com lock de líder no DynamoDB/Valkey) em vez de cron por minuto com ~20 processos | go-common/cdk | todos que usam o padrão; poupa CPU burstable e ~US$2–3/mês de SSM |
| CORS same-origin / preflight por URL | decisão de família (ADR 0013 foi revertido) | só vale se medir o impacto |

## 5. P3 — motor de cobrança para merchant grande (L, sem alarme, com SQS)

1. **Scheduler só enumera** (subscription, período) e põe numa fila SQS (dedupe = chave de geração).
   **Workers 16–64** com teto por tenant (2–4), FIFO por `org` onde a numeração tem de ser sem buracos.
2. **Contador sem buracos**: numerar lotes de até 25 faturas numa transação (contador += N) ou um consumidor
   por tenant. Hoje ~20–50 finalizações/s por tenant, e workers paralelos colidem após 12 tentativas.
3. **Webhooks de saída**: hoje um lote de 50 por minuto para a plataforma inteira, serial, FIFO, com timeout de
   10 s (um endpoint morto trava 8 min para todos, inclusive os produtos CTech). Fila SQS por endpoint
   (`MessageGroupId = org#endpoint`), 32–64 workers, backoff, DLQ, breaker por endpoint, descartar tenant sem
   endpoint antes de enfileirar, shard `hash(event_id)%16` no índice.
4. **Cobrança BYOK assíncrona**: criar a cobrança fora do sweep (fatura fica OPEN; o job horário já faz
   `ensure`). Token bucket por conexão (Inter ≈120/min), cobrança antecipada (D-3) para renovações conhecidas,
   consultar tentativas só após limiar e confiar no webhook do banco.
5. **Cache por execução** de preço, produto, org e guarda de tenant (preço é imutável).
6. **Webhooks de entrada** (liquidação ≈15 operações inline): fila na frente quando o P3.3 existir.

## 6. P4 — entitlement e uso

- **Entitlement** (chamado por todos os produtos): hoje 5–6 leituras por chamada, sem cache; a 10k rps seriam
  40–60 mil leituras/s e o item de preço quente passa de 3k RCU. Cache em processo (preço/produto/org, TTL longo
  para preço) + resposta por cliente em Valkey 15–30 s, invalidada pelo hook de realtime que já existe
  (`WithEntitlementHook`). Os consumidores cacheiam alguns segundos.
- **Uso**: tirar o middleware de idempotência de `/usage` (a chave de ordenação já deduplica; economiza 2
  escritas + 1 leitura por evento), aceitar lote de até 100, pré-agregar por minuto/hora com `ADD` atômico
  (até 44.640 linhas/mês por item em vez de milhões) e somar em streaming no fechamento. Hoje
  `ListForPeriod` carrega tudo em memória (1M eventos ≈ 300 MB). L, com migração (leitura dupla por um período).
- **Listagem/busca** de 100k+ faturas/clientes: GSIs esparsos por status e por cliente; sem OpenSearch por ora.

## 7. P5 — computação sem alarme

- **Escala agendada** (grátis, sem CloudWatch): subir antes da janela conhecida de renovação (sweep às 07:10
  UTC) e voltar depois; o excedente ao Savings Plan sai como on-demand por hora. Um script (no repositório,
  não em /tmp) pode ajustar a capacidade desejada lendo a profundidade da fila SQS (leitura de API, sem alarme).
- Com o trabalho em fila (P3), o escalonamento passa a ser por backlog, não por CPU.
- `t4g.small` (2 GB) a partir de ~10k usuários; `max_size` 6–10; **antes de escalar para mais de 1 instância**,
  confirmar eleição de líder e o job `deliver` (o comentário em `variables.tf:51-56` já avisa que viram carga).
- Frontend: virtualização (`@tanstack/react-virtual`, ~3 KB) só em extrato/lançamentos; `next/dynamic` para telas
  raras (QR, import CSV, gráficos). Sem custo de AWS.

## 8. Custo (estimativa, premissa ~50 req/usuário/dia, nano único)

| Usuários | Mensal aproximado | Principais linhas |
|---|---|---|
| 1 | ~US$6 | EC2 nano, SSM do cron por minuto |
| 1k | ~US$9 | + DynamoDB e logs |
| 10k | ~US$25 | t4g.small ou 2 nanos, DynamoDB US$5–8 |
| 100k | ~US$90–150 | 3–6 instâncias, DynamoDB US$30–60, logs ~US$15 |

Custo marginal das mudanças: SQS ≈ US$0,40 por milhão de requisições (centavos por 200k eventos); cache em
processo e batching **reduzem** leituras (~US$0,4 por milhão de requisições só de overhead de auth hoje); a
renovação custa ~US$7 por 100k assinaturas, o problema é latência, não dinheiro. PITR (+~80% de storage) e
Standard-IA em tabelas append-only (audit, ledger, webhooks) só pagam com dezenas de GB.

## 9. Ordem sugerida

1. **P0** (S, um PR por item, com teste que reproduz a falha).
2. **P2 cache de auth + deadline + GOMEMLIMIT + log filter** (ganho para toda a família, risco baixo).
3. **P1 extrato/queryPrefix/purge/export** (tira o risco de OOM do nano).
4. **P3 fila + workers** (maior esforço; começa por webhooks, depois renovação).
5. **P4**, depois **P5** quando houver mais de uma instância.

## 10. Não verificado

Nenhum item foi medido ou carregado sob teste. Premissas a confirmar: SUMMARY por conta/mês (base do saldo de
abertura), caller de `PendingOn`, valor real do `client_max_body_size`, estado vivo da AWS, tráfego real,
tamanho das linhas de log, volume de `levels`, `data_export`, endpoints da account.
