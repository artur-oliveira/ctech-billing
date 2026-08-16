# CTech Billing — Avaliação de Produto, Arquitetura e UX

Data: 2026-08-15
Status: **discovery / análise — nenhuma linha de código de produto foi escrita ou alterada**

Convenção de rotulagem usada no documento inteiro:

- **[FATO]** — verificado no código/documentação, com caminho de arquivo.
- **[REC]** — recomendação minha, com justificativa.
- **[HIP]** — hipótese que precisa de confirmação sua ou de dados que não existem ainda.
- **[DECIDIDO]** — decisão do dono do produto, tomada em 2026-08-15. Não rediscutir sem motivo novo.

---

## 0. Decisões registradas (2026-08-15)

### D1. Escopo do produto: **A e B ao mesmo tempo** [DECIDIDO]

O CTech Billing atende simultaneamente a CTech cobrando seus próprios clientes (A) e terceiros
cobrando os clientes deles (B). Isso é viável **como modelo de domínio único** — A é B com um
único tenant —, mas tem quatro consequências que não são negociáveis e que reescrevem partes
deste documento:

1. **`Organization` deixa de ser formalidade e vira dependência de Phase 0.** A recomendação
   original de "tenant = `product_key`" (§ 12.2, opção *a*) **morre aqui**. Ver § 12.2, e D4
   para o que fica adiado.
2. **A perna "dinheiro chega ao merchant" continua bloqueada.** **[FATO]** custódia por subconta
   está atrás de `AsaasCustodyEnabled`, default `false`
   (`ctech-wallet/api/internal/config/config.go:31`). **D3 resolve como tratar isso:** o caminho
   é construído e travado no servidor por `payout_status`, sem improviso de repasse. Merchant
   externo não é onboardável até o flag virar — limitação de produto que vai no contrato, não
   descoberta pelo merchant.
3. **PII, test/live mode e checkout hospedado sobem para o MVP.** Não são mais adiáveis: com
   integrador externo, os três deixam de ser refinamento e viram requisito de entrada.
4. **A postura regulatória vira item de agenda, não de backlog.** Assim que dinheiro de terceiro
   passar por infraestrutura da CTech, a discussão de facilitação de pagamento é obrigatória.
   D3 compra tempo — enquanto o gate estiver fechado, nenhum dinheiro de terceiro transita — mas
   não dispensa a conversa antes de ligar o flag. **[REC]** tarefa jurídica com prazo próprio; o
   engenheiro não decide isso, mas é quem descobre tarde.

**[REC] Sequência prática, dado A+B:** um único código, tenant genérico desde a primeira
migration, **tenant zero = produtos CTech** (é o primeiro cliente e o melhor teste), merchant
externo habilitado por flag por organização. A ordem não muda o *quê* — muda só quem entra
primeiro.

### D2. Rail de cobrança: **PIX direto na fatura** [DECIDIDO]

O consumidor paga a fatura por PIX, sem precisar pré-carregar saldo no wallet.

Consequências:

- **A dependência do § 3.3 vira o caminho crítico do projeto**, não uma tarefa de Phase 3.
  Estender o fluxo `product-purchase` do `ctech-wallet` para aceitar `amount_cents` do chamador
  M2M é o pré-requisito de qualquer demo com dinheiro real.
- **`POST /v1.0/internal/wallet/real/debit` continua útil**, mas como caminho *secundário*
  (cliente que já tem saldo). **[REC]** implementar os dois, com o débito de saldo como
  otimização tentada antes de abrir o PIX — nunca como única via, porque falha para todo cliente
  novo.
- **`CheckoutSession` sobe para o MVP** (§ 6.3): PIX tem QR, copia-e-cola e expiração; isso é
  uma sessão com estado, não um link.
- **A destinação do dinheiro difere entre A e B**: em A, conta pooled da CTech (funciona hoje);
  em B, subconta do merchant (bloqueado pelo flag de D1.2).

### D3. Merchant externo antes da custódia: **implementar, bloquear na ponta** [DECIDIDO]

O caminho "dinheiro cai na subconta do merchant" é construído no modelo e no código, mas
permanece inacessível enquanto `AsaasCustodyEnabled=false`
(`ctech-wallet/api/internal/config/config.go:31`).

Consequências:

- **Nenhuma improvisação de repasse.** A opção "pooled da CTech e repassa fora do sistema" está
  descartada. Era a que atraía discussão de facilitação de pagamento; some do desenho.
- **O bloqueio é de habilitação, não de código.** `Organization` ganha um estado de
  onboarding de recebimento (`payout_status`: `not_configured` | `pending_custody` |
  `enabled`), e toda rota que abre cobrança para org com `payout_status != enabled` responde
  `409` com motivo explícito. **[REC]** o gate mora numa única função de autorização de
  cobrança, não espalhado por handler.
- **O flag não é feature flag de UI.** Esconder botão não é bloquear. O bloqueio é servidor,
  a UI só reflete.
- **Merchant externo não é onboardável até o flag virar.** Isso precisa estar no contrato
  comercial, não ser descoberto pelo merchant no primeiro cadastro.

### D4. Primeiro merchant externo: **sem previsão** [DECIDIDO]

Era ideia, não compromisso. Isso muda o *cronograma* de D1, não o *modelo* de D1:

- **O que continua em Phase 0:** `organization_id` + `livemode` na chave primária de toda
  entidade. Custa quase nada agora, é migração de dados depois. Não negociável.
- **O que sai do caminho crítico:** a fase 0b (`Organization`/`Membership`/`Invitation` em
  `ctech-account`). Sem merchant externo com data, construir CRUD de organização, convite e
  papéis agora é código especulativo. **[REC]** Phase 0 cria a `Organization` como registro
  mínimo local ao billing (id, nome, `payout_status`, `livemode`) com **um** dono, e a
  migração para o `ctech-account` acontece quando existir merchant externo real ou quando o
  `ctech-dfe` precisar compartilhar organização — o que vier antes.
- **O risco que isso aceita:** a migração futura dessa `Organization` mínima para o account.
  É aceitável porque a chave primária já carrega `organization_id` — a migração é de
  propriedade do registro, não de reindexação de dados.
- **O que isso remove:** o maior risco de cronograma do projeto, que era mexer em repo de
  produção antes de existir cliente.

### D5. Vencimento em fim de semana/feriado: **roll-forward** [DECIDIDO]

Vencimento cai em sábado, domingo ou feriado nacional: anda para o próximo dia útil.

Consequências:

- **Fecha a pendência de `OVERVIEW.md:127-130`**, aberta desde o spec original.
- **O relógio de dunning conta a partir da data ajustada**, não da original. Caso contrário o
  cliente entra em atraso por um dia em que não podia pagar.
- **Roll-forward pode empurrar o vencimento para o mês seguinte** (vencimento dia 31/12 caindo
  em feriado). Isso é permitido e não altera o período de competência da fatura: `period_start`
  e `period_end` são calculados antes do ajuste e nunca se movem. Sem essa regra, doze faturas
  anuais viram onze ou treze.
- **A função é pura e testada isoladamente** (`ARCHITECTURE.md:80-89` já previa isso).
  Casos de teste obrigatórios: Carnaval, Sexta-Santa, Corpus Christi, virada de mês, virada de
  ano, e feriado colado em fim de semana (sexta feriado, vence na segunda).

### D6. Primeiro consumidor: **`ctech-dfe`, sem data** [DECIDIDO]

Confirmado como primeiro serviço com assinatura, sem data comprometida.

Consequências:

- **O MVP é validado contra consumidor real, não sintético** — mas o cronograma do billing não
  pode depender do cronograma do dfe. **[REC]** construir contra o contrato do dfe
  (dois planos: DF-e Basic fixo, DF-e Sob Demanda metered) e demonstrar com o próprio billing
  como tenant, sem esperar o dfe estar pronto para integrar.
- **A emissão automática de NFS-e no `invoice.paid` continua post-MVP**, e agora com razão
  melhor: acoplar o MVP ao ciclo fiscal do dfe atrasa os dois.

### D7. Datastore: **DynamoDB**, analytics por GSI de período [DECIDIDO]

Segue o padrão já provado no `ctech-dfe`.

**[FATO]** O padrão existe e funciona: `ctech-dfe/cdk/lib/dynamodb-stack.ts:92-110` define a
`dfe-index` com chave **multi-atributo** — partição `(pk, incoming)`, ordenação
`(year, month, day, ...)` — e `ctech-dfe/api/internal/repositories/documents.go:137-180`
monta a `KeyConditionExpression` por prefixo (só ano, ano+mês, ano+mês+dia). Isso exige
`aws-cdk-lib` com suporte a `partitionKeys`/`sortKeys` (dfe está em 2.265.0).

Consequências:

- **Encerra o ADR pendente de `ARCHITECTURE.md:8-12` e `PLAN.md:68`.** O ADR ainda deve ser
  escrito, mas para registrar a decisão e seus limites, não para reabri-la.
- **A chave primária fica `pk = {organization_id}#{livemode}`.** D1 e D5 e este item convergem
  aqui: essa é a decisão mais cara de retroagir do projeto inteiro.
- **A ordenação `year, month, day` é obrigatória nessa sequência.** Chave multi-atributo só
  aceita restrição por prefixo — trocar a ordem impede consulta por ano sem mês.
- **[REC] Guardar `year`/`month`/`day` como números derivados de `America/Sao_Paulo`**, não de
  UTC. Fatura vencendo 01/03 às 00:30 BRT é 28/02 em UTC; relatório mensal erra a virada.
- **Limite honesto que isso aceita:** essa GSI serve as métricas **pré-declaradas** do § 11
  (faturas emitidas, pagas, valor por período, taxa de sucesso). Não serve consulta ad-hoc,
  coorte por atributo arbitrário, nem junção entre entidades. Quando isso for pedido — e vai
  ser — a saída é exportar para S3 e consultar com Athena, **não** transformar o billing em
  banco relacional depois. Isso vira item de § 17 pós-MVP, não dívida escondida.
- **O que isso força no domínio:** agregação de `UsageRecord` e cálculo de pro-rata rodam em
  código Go sobre uma partição, não em SQL. Já era o desenho (`PLAN.md:20-22`); agora é
  obrigação. Partição de uso precisa ser por `{subscription_item_id}#{period}` para o
  fechamento ler uma partição só.

### D8. `metadata` chave/valor nas entidades principais [DECIDIDO]

Contrato completo em **§ 5.4**. Resumo das três regras que sustentam o resto:

- **Opaco.** Nenhuma regra de negócio do billing lê `metadata`. Dado que muda comportamento
  merece campo de primeira classe.
- **Copiado, não referenciado.** `Subscription.metadata` é copiado para a `Invoice` na geração;
  fatura é registro histórico e não pode ser reescrita por edição posterior da assinatura.
- **É superfície de LGPD** (§ 8): vai vazar PII não declarada em webhook se ninguém avisar e
  ninguém aplicar retenção. Mitigação no dia 1, não depois.

### D9. Chave do `AsaasCustodyEnabled`: **Artur, após jurídico + KYC testado** [DECIDIDO]

Quem liga: **Artur**. Pré-condições, todas obrigatórias, nenhuma dispensável por pressa:

1. Orientação jurídica sobre CTech intermediar recebimento de terceiro (a pergunta é se a
   operação configura arranjo de pagamento — quem responde é advogado, não este documento).
2. Fluxo completo de KYC + criação de subconta no Asaas **testado ponta a ponta**, não lido na
   doc do provedor.

**Dois gates distintos, não confundir** — este é o erro provável em revisão:

| Gate                                | Onde vive                                                        | O que trava                                                  |
| ----------------------------------- | ---------------------------------------------------------------- | ------------------------------------------------------------ |
| `AsaasCustodyEnabled`               | config do **wallet** (`ctech-wallet/api/internal/config/config.go:31`, default `false`) | capacidade de custódia existir no ecossistema                 |
| `Organization.payout_status` (D3)   | **billing**, por organização                                     | um merchant específico poder abrir cobrança                   |

Ligar o primeiro é **deploy do wallet**, não do billing. O segundo continua por merchant mesmo
depois: custódia ligada não é autorização automática de todo mundo. Um flag global que destrava
todos os merchants de uma vez é justamente o que D3 existe para impedir.

### D10. `Organization` mínima **sem CNPJ** [DECIDIDO — segue § 21.2]

Campos no MVP: `id`, `display_name`, `payout_status`, `livemode`, dono único. **Nada de CNPJ,
razão social, endereço ou certificado.** Motivo: no minuto em que o billing guarda cadastro de
empresa, a fronteira com o cadastro fiscal do `ctech-dfe` fica ambígua e passa a existir duas
fontes de verdade sobre a mesma empresa — e a segunda sempre desatualiza.

Consequência prática que precisa estar escrita: **a NFS-e que a CTech emite sobre a própria
receita não sai do billing.** O billing dispara `invoice.paid`; quem tem CNPJ, certificado e
regra fiscal é o `ctech-dfe` (§ D6). Se em algum momento alguém quiser "só um campinho de CNPJ
aqui para facilitar", isso é o começo da duplicação — recusar.

CNPJ entra junto com a migração para o `ctech-account` (Phase 6a), não antes.

### D11. Teto por cobrança na extensão do wallet: **100000 centavos (R$ 1.000,00)** [DECIDIDO]

Validado **no wallet**, no servidor, por cliente M2M, na extensão do product-purchase (§ 3.3).
Valor acima disso: rejeita com `422`, não trunca. Default do teto = 100000; configurável por
cliente M2M para quando existir contrato que justifique.

Três coisas que este número implica e que é melhor saber agora:

- **Ele limita o produto, não só a fraude.** Plano anual acima de R$ 1.000,00 cobrado de uma vez
  é rejeitado pelo próprio teto. Se um plano assim entrar no catálogo, ou o teto sobe para aquele
  cliente, ou a cobrança vira mensal. Isso é decisão de negócio consciente, não bug a descobrir
  em produção.
- **É teto por cobrança, não por período.** Não impede 100 cobranças de R$ 1.000,00. O que ele
  limita é o estrago de *uma* requisição errada ou forjada — que é exatamente o buraco aberto ao
  aceitar `amount_cents` do chamador. Teto agregado (por dia, por cliente) é defesa diferente:
  não construir agora, construir quando houver volume que a torne mensurável.
- **Não substitui idempotência nem escopo.** As três defesas de § 3.3 continuam valendo juntas:
  escopo do cliente M2M, teto, `Idempotency-Key` obrigatória.

### D12. Retenção e TTL [DECIDIDO — valores adotados]

Em DynamoDB (D7) o TTL é atributo gravado **na criação do item**. Mudar a política depois só
afeta item novo — o passado fica. Por isso o atributo entra desde o primeiro item escrito, mesmo
com prazo generoso; o que não pode é criar item sem ele e "definir depois".

| Registro                              | Retenção     | Motivo                                                                        |
| ------------------------------------- | ------------ | ----------------------------------------------------------------------------- |
| `Invoice`, `InvoiceItem`, `CreditNote`| **Sem TTL**  | Documento com piso legal de 5 anos; expurgo é processo revisado, não TTL       |
| `AuditLog`                            | **5 anos**   | Acompanha o piso do documento que ele explica                                  |
| `PaymentAttempt`                      | **5 anos**   | É a prova de por que a fatura está paga ou não; some junto com a fatura, não antes |
| `Subscription` cancelada              | **Sem TTL**  | Explica faturas que continuam existindo                                        |
| `Event` + `WebhookDelivery`           | **90 dias**  | Janela de reentrega/depuração. Fatura é a verdade; evento é notificação        |
| `CheckoutSession` `EXPIRED`/`CANCELED`| **90 dias**  | Serve para suporte ("paguei e não caiu"), não para contabilidade               |
| `UsageRecord` bruto                   | **24 meses** | Agregado já vive na fatura; o bruto é para auditar disputa de consumo          |
| `Customer` anonimizado (§ 8)          | **Sem TTL**  | O registro fica; o conteúdo identificável é que sai                            |

Duas ressalvas técnicas, não cosméticas:

- **TTL do DynamoDB não é prazo exato.** A AWS apaga tipicamente em até 48 h após o vencimento.
  Serve para higiene de dados; **não** serve como garantia de "apagado no dia X" para exigência
  legal de prazo duro. Se aparecer uma, o expurgo tem que ser job explícito.
- **`metadata` herda o TTL do item que o carrega** (§ 8). Não existe retenção separada para
  `metadata` — foi decidido assim de propósito: mais um prazo é mais uma coisa para esquecer.

---

## 1. Executive Summary

### 1.1 O achado central: existem dois produtos diferentes na mesa

**[FATO]** O repositório `ctech-billing` contém somente 4 documentos (`README.md`,
`OVERVIEW.md`, `ARCHITECTURE.md`, `PLAN.md`, 451 linhas no total, commit `d0ff629`). Não há
`api/`, `ui/`, `cdk/`, `cmd/`, `internal/`, migrations, testes ou schema. O próprio README
declara isso em `README.md:12-16`.

**[FATO]** O produto descrito nesses documentos e o produto descrito no seu briefing atual são
**produtos diferentes**, não versões do mesmo produto:

| Dimensão               | Spec no repo (`OVERVIEW.md`)                                                                            | Briefing atual                                                     |
|------------------------|---------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------|
| Quem cobra             | CTech cobra seus próprios clientes                                                                      | Terceiros cobram os clientes deles                                 |
| Tenant                 | `product_key` (`dfe`, `poker`)                                                                          | Organização/merchant externo                                       |
| Cliente                | `customer_ref` opaco, **zero PII** (`OVERVIEW.md:35`)                                                   | `Customer` completo: nome, e-mail, doc, endereço, idioma, timezone |
| Cobrança               | débito do saldo em `ctech-wallet`                                                                       | Checkout, payment links, métodos de pagamento                      |
| UI                     | **nenhuma** — "a dedicated `ctech-billing/ui` is unnecessary for MVP — YAGNI" (`ARCHITECTURE.md:19-20`) | Dois portais completos + Developers + Analytics                    |
| Descontos              | não existem no spec                                                                                     | Coupon, Promotion Code, Discount                                   |
| Superfície regulatória | nenhuma nova (dinheiro flui cliente → CTech)                                                            | **facilitação de pagamento / subadquirência**                      |

Essa última linha é a mais importante do documento inteiro. Ela não é um detalhe de escopo, é
uma mudança de categoria de empresa.

### 1.2 A decisão que precisa ser tomada antes de qualquer código

**[REC]** Você precisa escolher, agora, entre:

- **Produto A — Billing interno da CTech.** Sistema de registro de *o que é devido e por quê*
  para os produtos da própria CTech (`ctech-dfe`, `ctech-poker`, futuros). Cliente final paga a
  CTech. É o que o spec atual descreve, é o que o `ctech-dfe` precisa para monetizar, e é
  construível sem nenhuma licença nova.
- **Produto B — Billing platform (SaaS multi-merchant).** Terceiros usam a CTech para cobrar os
  clientes *deles*. É o que o briefing descreve. Exige: onboarding/KYB de merchant, custódia ou
  split de recebíveis, responsabilidade sobre chargeback/MED, contrato de facilitação, e — no
  Brasil — postura regulatória que hoje a CTech não tem montada.

**[DECIDIDO — ver § 0/D1] A e B ao mesmo tempo.** Um único código, `Organization` como cidadão de
primeira classe desde a primeira migration, tenant zero = os próprios produtos CTech, merchant
externo habilitado por flag por organização (D3). O que muda em relação à recomendação original: a
organização deixa de poder ser um apelido de `product_key`. O que **não** muda por D4: membros,
convites e papéis completos ficam para quando existir merchant externo real — o MVP tem
organização com um dono, provisionada à mão (§ 12.2).

Justificativa: **[FATO]** o rail que o Produto B exige não existe. `ctech-wallet` sabe debitar
saldo interno (`ctech-wallet/api/internal/api/v1/router.go:119`) e abrir cobranças PIX que caem
na conta *pooled da CTech* (`ctech-wallet/docs/plans/2026-08-12-product-purchase-skus.md:22`).
A custódia por subconta (Asaas), que é o que permitiria dinheiro cair na conta de um merchant
terceiro, está atrás de um feature flag desligado por padrão
(`ctech-wallet/api/internal/config/config.go:31`, `AsaasCustodyEnabled` default `false`). Sem
isso, o Produto B literalmente não tem para onde mandar o dinheiro do merchant.

### 1.3 As cinco recomendações de maior impacto

1. **Substituir `Plan` por `Product` + `Price` imutável.** Você perguntou; a resposta é sim,
   `Product + Price` é melhor, e o custo de trocar é zero porque nada foi construído. Elimina
   o mecanismo de `plan_version` + `grandfather_existing` do `OVERVIEW.md:149-155`, que é
   versionamento reinventado. Detalhe em § 5.
2. **Não construir API keys, OAuth clients, consent, MFA nem gestão de credencial no billing.**
   **[FATO]** `ctech-account` já entrega tudo isso: `GET/POST /v1.0/account/api-keys`,
   `/v1.0/account/oauth-clients`, `/v1.0/account/consents`, `/v1.0/account/activity`
   (`ctech-account/api/ENDPOINTS.md:290-313`), mais um registro dinâmico de scopes por serviço
   (`ctech-account/api/internal/scopes/catalog.go`, scope
   `internal:account:scope-registry:write`). A seção Developers do billing deve *linkar* para
   o account, não duplicá-lo.
3. **Estados derivados não são estados.** `overdue` e `pending` não devem existir na máquina de
   estados de Invoice — são consultas sobre `OPEN` + `due_date`. `refunded` também não: correção
   de fatura paga é `CreditNote`, que o spec já acertou (`OVERVIEW.md:70-74`). Detalhe em § 6.
4. **Créditos:** só existe uma forma de crédito segura dentro de billing — crédito **escopado a
   fatura, não sacável, não transferível, sem valor de face resgatável**. Qualquer coisa com
   saldo que o usuário possa converter em dinheiro é `ctech-wallet`. Detalhe em § 9.
5. **Multi-tenancy: não copie o modelo de organização do `ctech-dfe`.** **[FATO]** `ctech-dfe`
   tem organizações, membros, convites e RBAC completo
   (`ctech-dfe/api/internal/repositories/organizations.go`, `organization_users.go`,
   `roles.go`, `organization_invitations.go`); `ctech-account` **não tem organização nenhuma**
   (`ctech-account/api/internal/domain/` = apikey, audit, kyc, mfa, oauth, risk, session, user).
   Copiar o modelo do dfe para o billing cria a terceira linhagem do mesmo código — exatamente
   o erro que `_analysis/cross-stack-duplication.md` documenta ter acontecido duas vezes.

### 1.4 Uma correção factual no material de referência

**[FATO]** `_analysis/GENERAL-REPORT.md:74` afirma: "`ctech-wallet` has no real-fund debit/hold
endpoint at all yet — `ctech-billing` and `ctech-poker`'s real-money mode are both blocked on
this". **Isso está desatualizado.** A rota existe e está registrada:
`POST /v1.0/internal/wallet/real/debit`, scope `internal:wallet:debit-real`, handler em
`ctech-wallet/api/internal/api/v1/internal.go:36`, serviço em
`ctech-wallet/api/internal/services/wallet.go:1532`. O `README.md:28-31` do billing já reflete a
realidade correta. O bloqueio real é outro e mais específico — ver § 3.3.

---

## 2. Current State Assessment

### 2.1 O que existe neste repositório

**[FATO]** Inventário completo:

| Arquivo           | Linhas | Conteúdo                                                                               |
|-------------------|--------|----------------------------------------------------------------------------------------|
| `README.md`       | 36     | Posicionamento, relação com outros serviços, aviso de "design-only"                    |
| `OVERVIEW.md`     | 226    | Spec funcional: entidades, ciclos, pró-rata, feriados, MVP, inconsistências conhecidas |
| `ARCHITECTURE.md` | 121    | Stack, fronteiras, contrato proposto com wallet, calendário, scheduler, segurança      |
| `PLAN.md`         | 68     | 6 fases (0–5) + decisões abertas                                                       |

Não existe: código, testes, schema, migrations, CDK, CI, ADRs, `docs/`, frontend, design system.

### 2.2 Matriz de funcionalidades (rigor: nada marcado como existente sem caminho de arquivo)

| Funcionalidade                           | Documentada                                            | Backend | API | Frontend | Testes | Status                             |
|------------------------------------------|--------------------------------------------------------|---------|-----|----------|--------|------------------------------------|
| Plan (FIXED/METERED)                     | Sim (`OVERVIEW.md:13-30`)                              | Não     | Não | Não      | Não    | **Missing**                        |
| Plan versioning                          | Sim (`OVERVIEW.md:149-155`)                            | Não     | Não | Não      | Não    | **Missing** (e ver § 5 — repensar) |
| Subscription                             | Sim (`OVERVIEW.md:31-43`)                              | Não     | Não | Não      | Não    | **Missing**                        |
| Invoice                                  | Sim (`OVERVIEW.md:45-57`)                              | Não     | Não | Não      | Não    | **Missing**                        |
| UsageRecord                              | Sim (`OVERVIEW.md:59-68`)                              | Não     | Não | Não      | Não    | **Missing**                        |
| CreditNote                               | Sim (`OVERVIEW.md:70-74`)                              | Não     | Não | Não      | Não    | **Missing**                        |
| Pró-rata                                 | Sim (`OVERVIEW.md:132-147`)                            | Não     | Não | Não      | Não    | **Missing**                        |
| Calendário de feriados BR                | Sim (`ARCHITECTURE.md:80-89`)                          | Não     | Não | Não      | Não    | **Missing**                        |
| Ciclos (FIXED_MONTHLY / VARIABLE_ANCHOR) | Sim (`OVERVIEW.md:90-110`)                             | Não     | Não | Não      | Não    | **Inconsistent** (ver § 2.3)       |
| Scheduler de faturamento                 | Sim (`ARCHITECTURE.md:91-104`)                         | Não     | Não | Não      | Não    | **Missing**                        |
| Integração de cobrança com wallet        | Sim (`ARCHITECTURE.md:48-78`)                          | Não     | Não | Não      | Não    | **Missing + bloqueado** (§ 3.3)    |
| Dunning                                  | Sim, como *sugestão* (`OVERVIEW.md:177-180`)           | Não     | Não | Não      | Não    | **Missing**                        |
| Webhooks de saída                        | Sim, como *sugestão* (`OVERVIEW.md:181-183`)           | Não     | Não | Não      | Não    | **Missing**                        |
| Audit log                                | Sim, como *sugestão* (`OVERVIEW.md:187-189`)           | Não     | Não | Não      | Não    | **Missing**                        |
| Idempotência em M2M                      | Sim (`OVERVIEW.md:184-186`)                            | Não     | Não | Não      | Não    | **Missing**                        |
| Customer (entidade)                      | **Não** — spec nega explicitamente (`OVERVIEW.md:35`)  | Não     | Não | Não      | Não    | **Missing / conflito de spec**     |
| Checkout / payment links                 | Não                                                    | Não     | Não | Não      | Não    | **Missing**                        |
| Coupon / Discount                        | Não                                                    | Não     | Não | Não      | Não    | **Missing**                        |
| Analytics / MRR                          | Não                                                    | Não     | Não | Não      | Não    | **Missing**                        |
| Test mode / live mode                    | Não                                                    | Não     | Não | Não      | Não    | **Missing**                        |
| Multi-tenancy / RBAC                     | Parcial (`product_key` scoping, `OVERVIEW.md:190-192`) | Não     | Não | Não      | Não    | **Missing**                        |
| Invoice PDF                              | Sim, adiado (`OVERVIEW.md:193-194`)                    | Não     | Não | Não      | Não    | **Missing (deliberado)**           |
| Multi-moeda                              | Sim, fora de escopo (`OVERVIEW.md:198`)                | Não     | Não | Não      | Não    | **Missing (deliberado)**           |

Resumo honesto: **1 item Inconsistent, 0 Complete, 0 Partial, todo o resto Missing.** Não há
código para auditar. Não existe TODO/FIXME/mock/hardcode porque não existe implementação.

### 2.3 Inconsistências no material existente

1. **[FATO] `FIXED_MONTHLY` torna `billing_timing` um campo morto.** `OVERVIEW.md:26` define
   default `ADVANCE` para FIXED; `OVERVIEW.md:92-101` diz que em `FIXED_MONTHLY` o valor
   armazenado é ignorado. O doc reconhece e "resolve" a tensão em prosa, mas o resultado é um
   campo persistido cujo valor às vezes não significa nada — origem clássica de bug de leitura.
   **[REC]** eliminar o campo: derive a direção do par (`cycle_type`, `price.usage_type`), ou
   remova `FIXED_MONTHLY` como `cycle_type` e represente-o como `VARIABLE_ANCHOR` com
   `anchor=1º dia útil` + `ARREARS`. A segunda opção deixa **um** modelo de ciclo em vez de dois.
2. **[FATO] Datastore indefinido** (`OVERVIEW.md:210-213`, `ARCHITECTURE.md:8-12`).
   **Resolvido em D7:** DynamoDB, com analytics por GSI de período no padrão do `ctech-dfe`, e
   export para S3/Athena como saída declarada para consulta ad-hoc.
3. **[FATO] Roll-forward vs roll-backward de vencimento** não confirmado com o dono do negócio
   (`OVERVIEW.md:127-130`). **Resolvido em D5:** roll-forward.
4. **[FATO] Conflito documento vs briefing sobre PII.** `OVERVIEW.md:35` e `ARCHITECTURE.md:119`
   proíbem PII no billing ("keep `ctech-billing` boring from a data-breach-impact
   perspective"). O briefing pede nome, e-mail, telefone, documento e endereço do cliente. São
   posições incompatíveis; ver § 8 para a saída.
5. **[FATO] O `ARCHITECTURE.md:17-20` decide "sem frontend"**, o briefing pede dois portais.

### 2.4 O que existe no ecossistema e é reaproveitável (isto é o mais valioso do assessment)

| Capacidade                                                                                              | Onde está                                                                                   | Status                                                 | Uso pelo billing                                                         |
|---------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------|--------------------------------------------------------|--------------------------------------------------------------------------|
| OIDC + user token + M2M client_credentials                                                              | `ctech-account/api`                                                                         | **Implementado** (`ENDPOINTS.md:163-232`)              | Auth inteira. Não construir nada.                                        |
| API keys com scopes, rotação, step-up                                                                   | `ctech-account` `/v1.0/account/api-keys`                                                    | **Implementado**                                       | Seção Developers linka; não duplicar.                                    |
| OAuth clients self-service                                                                              | `ctech-account` `/v1.0/account/oauth-clients`                                               | **Implementado**                                       | Idem.                                                                    |
| Registro dinâmico de scopes por serviço                                                                 | `ctech-account/api/internal/scopes/`                                                        | **Implementado** (dfe e wallet já registram)           | Billing registra `billing:*`.                                            |
| Audit/activity do usuário                                                                               | `ctech-account` `/v1.0/account/activity`                                                    | **Implementado**                                       | Referência de padrão; billing precisa do seu próprio audit por entidade. |
| Débito de saldo real (M2M, idempotente)                                                                 | `ctech-wallet` `POST /v1.0/internal/wallet/real/debit`                                      | **Implementado** (`router.go:119`)                     | Cobrança de fatura contra saldo.                                         |
| Cobrança PIX avulsa com webhook de volta ao cliente M2M                                                 | `ctech-wallet` product-purchase (`internal/services/product_purchase.go`, `m2m_webhook.go`) | **Implementado, mas valor vem de catálogo fixo em Go** | Base do checkout — precisa de valor dinâmico (§ 3.3).                    |
| Custódia por subconta (Asaas), onboarding, transfer                                                     | `ctech-wallet/internal/asaas/`, `api/v1/baas.go`                                            | **Parcial, flag off** (`config.go:31`)                 | Pré-requisito do Produto B.                                              |
| Módulo Go compartilhado (dynamo, cache, problem, lock, oauth2client, ws)                                | `ctech-go-common` (`gopkg.aoctech.app/api-commons`)                                         | **Implementado**                                       | Base obrigatória do backend — evita a 3ª linhagem.                       |
| Constructs CDK (EC2 privada sem NAT, frontend Next estático, ALB, roles)                                | `ctech-cdk/lib/private-ipv4-ec2-service.ts`, `nextjs-static-frontend.ts`                    | **Implementado**                                       | Infra inteira. Não reescrever.                                           |
| Organizações + membros + convites + RBAC (OWNER/ADMIN/USER/VIEWER, permissões `ação.recurso`)           | `ctech-dfe/api/internal/repositories/roles.go:18-48`                                        | **Implementado, porém acoplado ao domínio fiscal**     | Modelo de referência — **não copiar**, ver § 12.2.                       |
| Design system (Next 16 + React 19 + Tailwind 4 + shadcn + Geist), com tokens versionados em `DESIGN.md` | `ctech-account/ui/DESIGN.md`, `ctech-dfe/ui/DESIGN.md`                                      | **Implementado, 2 temas distintos**                    | Convenção a seguir; tokens próprios para billing.                        |
| Cliente OAuth de frontend (PKCE, refresh single-flight)                                                 | `ctech-oauth-client`                                                                        | **Implementado**                                       | Auth da UI.                                                              |

---

## 3. Gap Analysis

### 3.1 Gaps dentro do escopo do spec atual (Produto A)

Tudo listado na matriz de § 2.2 como Missing. Ordem de risco:

1. **Pró-rata + cálculo de vencimento** — maior densidade de bug por linha do produto inteiro.
   O spec já manda tratar como função pura testada (`PLAN.md:20-25`); está correto.
2. **Idempotência de geração de fatura** — a chave proposta
   `{subscription_id}:{period_start}:{plan_version}` (`OVERVIEW.md:57`) muda de forma se
   `plan_version` sumir com Product+Price; nova chave em § 5.
3. **Reconciliação** — o spec já prevê que webhook não pode ser o único sinal
   (`ARCHITECTURE.md:67-71`). Correto e não negociável.
4. **Audit log e dunning estão marcados como "sugestão"** (`OVERVIEW.md:169-189`). **[REC]**
   promova ambos a requisito: sem dunning, uma cobrança falha produz fatura não paga para
   sempre; sem audit, suporte é impossível.

### 3.2 Gaps que só aparecem com o briefing novo (Produto B)

Nenhum destes tem sequer documentação hoje: `Customer` com PII, checkout session, payment link,
método de pagamento, cupom/desconto, portal do consumidor, console do merchant, analytics,
test/live mode, RBAC de organização, webhooks de saída com UI de inspeção, API logs, versionamento
de API, SDK, rate limiting por tenant, exportação de dados, retenção/LGPD operacional.

### 3.3 O bloqueio real, descrito com precisão

**[FATO]** Para cobrar uma fatura hoje existem exatamente dois caminhos em `ctech-wallet`:

- `POST /v1.0/internal/wallet/real/debit` — **exige que o cliente já tenha saldo**. É um débito
  de carteira, não uma cobrança. Se o saldo é insuficiente, falha; não abre PIX, não gera boleto.
- Fluxo product-purchase — abre cobrança PIX de verdade, notifica de volta por webhook, **mas o
  valor vem de um catálogo fixo compilado em Go, sem caminho de escrita administrativo**
  (`ctech-wallet/docs/plans/2026-08-12-product-purchase-skus.md:28`), e o dinheiro cai na conta
  pooled da CTech, sem efeito em ledger.

**[REC]** O menor contrato novo que desbloqueia o billing não é o `POST /v1.0/charges` genérico
do `ARCHITECTURE.md:51`. É **uma generalização do product-purchase que já existe**: aceitar
`amount_cents` fornecido pelo chamador M2M (em vez de SKU de catálogo), com
`Idempotency-Key = invoice_id`, `metadata.invoice_id`, e reusar exatamente o webhook de
notify-back e o mecanismo de txid determinístico já implementados. Isso é uma extensão de um
fluxo testado em produção, não um subsistema novo — e é ~1/5 do trabalho do contrato proposto.

Risco a mitigar explicitamente com o time do wallet: permitir valor arbitrário vindo do
chamador remove a validação "valor pertence ao catálogo" que hoje é uma defesa anti-fraude.
A substituição correta é: valor assinado pelo escopo do cliente M2M + teto por cliente +
idempotência obrigatória. **Teto decidido em D11: 100000 centavos (R$ 1.000,00) por cobrança**,
validado no wallet, `422` acima disso. Note que isso põe um limite de produto junto: plano anual
acima de R$ 1.000,00 não passa sem elevar o teto daquele cliente.

---

## 4. Competitive Analysis

Fontes: documentação pública dos produtos e padrões observáveis nas suas APIs. **[HIP]** onde
indico "tendência", é leitura minha do mercado, não fato verificável no seu repositório.

### 4.1 O que virou padrão de fato (aparece em todos)

- **Product + Price** (Stripe, Chargebee, Recurly, Lemon Squeezy) venceram `Plan` monolítico.
  Preço imutável, produto mutável.
- **Invoice como documento imutável após emissão**, correções por credit note. Universal.
- **Webhook assinado com HMAC + timestamp + janela de replay**, com log de entregas e retry
  manual. Universal. Ninguém adotou WebSub.
- **Idempotency-Key em todo POST mutante.** Universal.
- **Test mode / live mode com credenciais e dados separados.** Stripe, Paddle, Chargebee,
  Recurly, Adyen.
- **Customer portal hospedado** (não um "dashboard simplificado") — Stripe Customer Portal,
  Chargebee Portal, Paddle. Consistente com sua intuição no briefing.
- **Dunning configurável** (nº de tentativas, intervalos, ação final). Universal.

### 4.2 O que é complexidade que a maioria não precisa

- Revenue recognition / ASC 606 (Chargebee, Recurly). Só faz sentido com auditoria externa.
- Motor de impostos multi-jurisdição (Paddle/Stripe Tax). No Brasil, isso é NFS-e — domínio do
  `ctech-dfe`, não do billing.
- Entitlements engine como produto separado. Vira feature flag glorificada em 90% dos casos.
- Quote-to-cash / CPQ. É vendas, não billing.

### 4.3 Tabela comparativa

| Feature                           | CTech atual                  | Stripe | Paddle    | Chargebee            | Outros         | Recomendação CTech                                  |
|-----------------------------------|------------------------------|--------|-----------|----------------------|----------------|-----------------------------------------------------|
| Product + Price                   | Não (`Plan` + versão)        | Sim    | Sim       | Sim (Item/ItemPrice) | Recurly, Lemon | **Adotar Product+Price** — MVP                      |
| Preço imutável                    | Não (versão mutável)         | Sim    | Sim       | Sim                  | —              | **Adotar** — MVP                                    |
| Customer com PII                  | Não (`customer_ref`)         | Sim    | Sim       | Sim                  | —              | **Adotar mínimo** (§ 8) — MVP                       |
| Invoice state machine             | 5 estados documentados       | 6      | 5         | 6                    | —              | **5 estados, sem derivados** — MVP                  |
| Credit note                       | Sim (documentado)            | Sim    | Sim       | Sim                  | —              | **Manter** — MVP                                    |
| Subscription lifecycle            | 5 estados                    | 8      | 5         | 7                    | —              | **6 estados** (§ 6.2) — MVP                         |
| Trials                            | Sim (`trial_days`)           | Sim    | Sim       | Sim                  | —              | **MVP**                                             |
| Proration                         | Sim (documentado)            | Sim    | Parcial   | Sim                  | —              | **MVP**                                             |
| Metered/usage billing             | Sim (`METERED`)              | Sim    | Sim       | Sim                  | —              | **MVP** (é requisito do dfe)                        |
| Base + overage híbrido            | Adiado (`OVERVIEW.md:87-88`) | Sim    | Sim       | Sim                  | —              | **Post-MVP**, mas modelar SubscriptionItem já       |
| Checkout hospedado                | Não                          | Sim    | Sim       | Sim                  | Lemon, MP      | **MVP (hospedado apenas)**                          |
| Checkout embutido/JS              | Não                          | Sim    | Sim       | Parcial              | Adyen          | **Não recomendado agora** — superfície PCI/XSS      |
| Payment links                     | Não                          | Sim    | Sim       | Sim                  | Lemon          | **Post-MVP** (é checkout session sem expiração)     |
| Coupons                           | Não                          | Sim    | Sim       | Sim                  | —              | **Post-MVP** (MVP: `Discount` direto na assinatura) |
| Promotion codes                   | Não                          | Sim    | Sim       | Sim                  | —              | **Future** — só com self-serve real                 |
| Dunning configurável              | Sugerido                     | Sim    | Sim       | Sim (avançado)       | Recurly        | **MVP mínimo** (§ 6.2)                              |
| Webhooks assinados + delivery log | Sugerido                     | Sim    | Sim       | Sim                  | —              | **MVP**                                             |
| Reenvio manual de evento          | Não                          | Sim    | Sim       | Sim                  | —              | **Phase 3**                                         |
| API logs / request ID             | Não                          | Sim    | Parcial   | Parcial              | —              | **Phase 3**                                         |
| Test/live mode                    | Não                          | Sim    | Sim       | Sim                  | Adyen, MP      | **Decidir no Phase 0** (§ 12.4)                     |
| Customer portal                   | Não                          | Sim    | Sim       | Sim                  | —              | **Phase 2**                                         |
| MRR/ARR/churn                     | Não                          | Sim    | Sim       | Sim                  | —              | **Phase 4** (§ 11)                                  |
| Invoice PDF                       | Adiado                       | Sim    | Sim       | Sim                  | —              | **Post-MVP** — no BR o doc legal é NFS-e            |
| Tax engine                        | Fora de escopo               | Sim    | Sim (MoR) | Parcial              | —              | **Não construir** — delegar ao `ctech-dfe`          |
| Merchant of Record                | Não                          | Não    | **Sim**   | Não                  | Lemon          | **Não construir** — muda a natureza da empresa      |
| Multi-moeda                       | Fora de escopo               | Sim    | Sim       | Sim                  | —              | **Future** — schema permite, resto não              |
| Disputes/chargebacks              | Não                          | Sim    | Sim       | Parcial              | Adyen          | **Domínio do wallet/PSP** — não do billing          |
| Revenue recognition               | Não                          | Sim    | Parcial   | Sim                  | Recurly        | **Não recomendado**                                 |

**Padrão que vale copiar explicitamente:** a separação Stripe entre *o que aconteceu*
(`Event`, imutável, com ID e versão) e *o que foi entregue* (`WebhookDelivery`, mutável,
retentável). Quase todo mundo que erra webhook errou por fundir as duas coisas.

**Padrão que não vale copiar:** os 8 estados de subscription do Stripe. `incomplete` e
`incomplete_expired` existem porque o Stripe cobra cartão de forma síncrona com 3DS. Seu rail é
PIX/saldo assíncrono; o mesmo problema se resolve com `PAST_DUE` + dunning.

### 4.4 WebSub vs webhooks tradicionais

**[REC] Use webhooks tradicionais assinados. Não use WebSub.** Razões concretas, não estéticas:

| Critério                    | WebSub                                                | Webhook assinado (Stripe-style)           |
|-----------------------------|-------------------------------------------------------|-------------------------------------------|
| Autenticidade da mensagem   | HMAC opcional, negociado no subscribe                 | HMAC obrigatório por payload              |
| Registro do endpoint        | Handshake `hub.challenge`, estado distribuído         | Registro explícito na UI/API, estado seu  |
| Filtro por tipo de evento   | Por *topic URL* — vira uma URL por tipo               | Lista de tipos por endpoint, um endpoint  |
| Renovação de assinatura     | `hub.lease_seconds` expira; assinante precisa renovar | Sem expiração                             |
| Replay protection           | Não especificado                                      | Timestamp + janela + nonce                |
| Debug                       | Fora do escopo do spec                                | Delivery log é padrão de mercado          |
| Familiaridade do integrador | Baixa                                                 | Alta — todo dev de pagamentos já integrou |

WebSub foi desenhado para feeds públicos com muitos assinantes anônimos. Billing tem poucos
endpoints, todos autenticados e contratuais. É a forma errada de problema.

---

## 5. Domain Model (recomendado)

### 5.1 Diagrama conceitual

```
Organization (tenant; pk = {organization_id}#{livemode}; payout_status)
 ├─ Member ──> account.user_id           (RBAC; migra p/ ctech-account sob gatilho — D4)
 ├─ ApiCredential ──> account.oauth_client / api_key   (referência, não cópia)
 ├─ Customer
 │   ├─ Subscription
 │   │   └─ SubscriptionItem ──> Price
 │   ├─ Invoice
 │   │   ├─ InvoiceItem  (period, quantity, unit_amount, proration flag)
 │   │   ├─ Discount     (aplicação de um Coupon; Phase 2)
 │   │   ├─ PaymentAttempt ──> wallet charge
 │   │   └─ CreditNote
 │   └─ CheckoutSession ──> Invoice | Price
 ├─ Product ──> Price[] (imutáveis)
 ├─ Coupon (Phase 2)
 ├─ UsageRecord ──> SubscriptionItem
 ├─ WebhookEndpoint ──> WebhookDelivery ──> Event
 ├─ Event  (imutável, versionado, fonte de todo webhook)
 └─ AuditLog

metadata: map[string]string  (§ 5.4) em Customer, Subscription, Invoice,
                              Product, Price, CheckoutSession, CreditNote
```

### 5.2 Justificativa entidade por entidade (nada entra por imitação)

| Entidade                                 | Existe por quê                                                                                                                                                                          | Fase                                     |
|------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------|
| `Organization`                           | Sem ela, todo isolamento vira um `if product_key ==`. É a única entidade que é cara de adicionar depois.                                                                                | **Phase 0**                              |
| `Customer`                               | Fatura precisa de destinatário nomeável e e-mail para notificar. `customer_ref` opaco não permite mandar e-mail de vencimento.                                                          | **Phase 1**                              |
| `Product`                                | Agrupa preços de uma mesma coisa vendida; é o que aparece na fatura.                                                                                                                    | **Phase 1**                              |
| `Price`                                  | **Imutável.** Substitui `plan_version`. Mudar preço = criar `Price` novo; assinaturas antigas continuam apontando para o antigo — grandfathering vira consequência do modelo, não flag. | **Phase 1**                              |
| `Subscription`                           | Núcleo da recorrência.                                                                                                                                                                  | **Phase 1**                              |
| `SubscriptionItem`                       | Permite base+overage e mudança de quantidade sem redesenhar. **MVP: exatamente 1 item por assinatura, imposto na API, não no schema.**                                                  | **Phase 1 (schema) / Phase 4 (N itens)** |
| `Invoice`                                | Documento comercial imutável após emissão.                                                                                                                                              | **Phase 1**                              |
| `InvoiceItem`                            | Linha com período próprio — é o que torna pró-rata auditável (duas linhas, nunca uma linha líquida — `OVERVIEW.md:144-146` já acertou).                                                 | **Phase 1**                              |
| `PaymentAttempt`                         | Uma fatura pode ter N tentativas. Fundir tentativa com fatura é o erro que impossibilita dunning e diagnóstico.                                                                         | **Phase 1**                              |
| `CreditNote`                             | Única forma de corrigir fatura paga. Já no spec.                                                                                                                                        | **Phase 1**                              |
| `UsageRecord`                            | Requisito do dfe (`OVERVIEW.md:82-83`). Idempotência obrigatória.                                                                                                                       | **Phase 1**                              |
| `Event`                                  | Imutável, versionado, com ID. Fonte única de webhook, timeline de UI e replay.                                                                                                          | **Phase 2**                              |
| `WebhookEndpoint` / `WebhookDelivery`    | Separadas do `Event` (§ 4.3).                                                                                                                                                           | **Phase 2**                              |
| `CheckoutSession`                        | Torna "pagar esta fatura" um recurso com estado e expiração, em vez de um link mágico.                                                                                                  | **Phase 2**                              |
| `Coupon` + `Discount`                    | `Coupon` = a regra; `Discount` = a aplicação daquela regra a uma assinatura/fatura, com histórico. Duas entidades bastam.                                                               | **Phase 3**                              |
| `PromotionCode`                          | Só ganha razão de existir quando um humano digita um código num checkout self-serve.                                                                                                    | **Future**                               |
| `AuditLog`                               | Quem fez o quê. Separado dos logs de aplicação (`ARCHITECTURE.md:108-110` já acertou).                                                                                                  | **Phase 1**                              |
| `Notification`                           | **Não como entidade própria.** Ver § 10.                                                                                                                                                | —                                        |
| `Payment` (separado de `PaymentAttempt`) | **Não.** O pagamento de verdade mora no wallet; billing guarda tentativas e o resultado. Criar `Payment` no billing é começar a construir um ledger paralelo.                           | —                                        |
| `PaymentMethod`                          | **Não.** Instrumento de pagamento é wallet/PSP. Billing referencia, nunca armazena.                                                                                                     | —                                        |

### 5.3 Chave de idempotência revisada

Com `Price` imutável, a chave de `OVERVIEW.md:57` fica:
`{subscription_item_id}:{period_start}` — `plan_version` some porque o preço já é imutável e está
pinado no item. Mais curta, mais estável, e imune à renumeração de versão.

### 5.4 `metadata` — chave/valor livre por entidade [DECIDIDO — D8]

Lista chave/valor anexável a `Customer`, `Subscription`, `Invoice`, `Price`, `Product`,
`CheckoutSession` e `CreditNote`. É a peça que evita o pior padrão de billing: adicionar coluna
toda vez que um integrador precisa carregar um dado que só faz sentido pra ele.

Caso de uso citado: anexar referência de NF-e/recibo a uma cobrança.

**Contrato:**

| Aspecto        | Regra                                                                    |
|----------------|--------------------------------------------------------------------------|
| Tipo           | `map[string]string` — **sempre string**, nunca aninhado, nunca tipado     |
| Limites        | ≤ 50 chaves; chave ≤ 40 chars; valor ≤ 500 chars                          |
| Semântica      | **Opaco para o billing.** Nenhuma regra de negócio lê `metadata`          |
| Escrita        | Merchant/integrador via API e console. Substituição por chave, não merge cego |
| Leitura        | Devolvido em toda resposta da entidade; **propagado em todo webhook**     |
| Herança        | `Subscription.metadata` **é copiado** para a `Invoice` gerada, no momento da geração |
| Audit          | Alteração de `metadata` gera entrada de audit como qualquer outro campo   |
| Visibilidade   | **Nunca renderizado no portal do consumidor nem na fatura pública**       |

**Por que "opaco" é regra e não recomendação:** no dia em que uma decisão de cobrança olhar
`metadata["skip_dunning"]`, o campo livre virou schema informal sem validação, sem migração e sem
teste. Se um dado precisa mudar comportamento, ele merece campo de primeira classe. Essa é a
única linha que mantém `metadata` útil em vez de virar dívida.

**Herança copiada, não referenciada:** fatura é registro histórico. Se ela apontasse para o
`metadata` vivo da assinatura, editar a assinatura reescreveria o passado de faturas fechadas.

**[REC] Contra-proposta parcial ao exemplo da NF-e.** Referência fiscal em `metadata` funciona
para *anexar* um número que alguém digitou. Mas se o `ctech-dfe` vai emitir NFS-e a partir de
`invoice.paid` (D6), essa ligação vira relação de sistema — precisa de índice, de reconciliação
e de estado ("emitida", "falhou", "cancelada"). **Nasce em `metadata` agora**; quando a emissão
automática entrar, promove para campo próprio (`Invoice.fiscal_document_ref`) com GSI. Não
inverter a ordem: campo dedicado antes de existir o fluxo é especulação.

**Busca por `metadata`:** não no MVP. Em DynamoDB (D7) filtrar por valor de mapa é `Scan` — proibido
por § 12.1. Quando virar necessidade real, a saída é GSI esparsa sobre chaves **declaradas** pelo
merchant (`indexed_metadata_keys` na organização), não busca genérica.

**Nome:** `metadata`, não `tags` nem `custom_fields`. `tags` no mercado significa lista de rótulos
sem valor; `custom_fields` costuma implicar definição de schema e validação — nenhum dos dois é o
que isto é.

---

## 6. State Machines

Regra transversal **[REC]**: transição de estado é **uma função única por entidade**
(`func (i *Invoice) Transition(to Status, cause Cause) ([]Event, error)`), que valida, grava
audit e devolve os eventos a emitir. Nenhum `status = X` espalhado em handler. Isso é a diferença
entre um billing auditável e um que ninguém consegue explicar.

### 6.1 Invoice

**Estados (5):** `DRAFT` · `OPEN` · `PAID` · `VOID` · `UNCOLLECTIBLE`

**Não são estados:** `pending` (= `OPEN`), `overdue` (= `OPEN` ∧ `due_date < hoje` — atributo
derivado, exibido na UI, nunca persistido), `refunded` (= existe `CreditNote` que cobre o total).

| De → Para                | Gatilho                                                         | Side effects                                                                   | Evento                   |
|--------------------------|-----------------------------------------------------------------|--------------------------------------------------------------------------------|--------------------------|
| — → `DRAFT`              | scheduler cria fatura do período                                | calcula linhas, aplica descontos                                               | `invoice.created`        |
| `DRAFT` → `OPEN`         | finalização (automática pelo scheduler, ou manual)              | congela linhas, calcula vencimento, dispara notificação, abre `PaymentAttempt` | `invoice.finalized`      |
| `DRAFT` → `VOID`         | cancelamento antes de emitir                                    | —                                                                              | `invoice.voided`         |
| `OPEN` → `PAID`          | `PaymentAttempt` bem-sucedida (webhook wallet ou reconciliação) | `paid_at`, libera entitlement, dispara NFS-e (`OVERVIEW.md:171-176`)           | `invoice.paid`           |
| `OPEN` → `OPEN`          | tentativa falhou                                                | agenda retry, contador de dunning++                                            | `invoice.payment_failed` |
| `OPEN` → `UNCOLLECTIBLE` | dunning esgotado                                                | assinatura → `CANCELED` (se política mandar)                                   | `invoice.uncollectible`  |
| `OPEN` → `VOID`          | erro de emissão detectado antes de qualquer pagamento           | —                                                                              | `invoice.voided`         |
| `PAID` → *(nenhum)*      | —                                                               | correção só via `CreditNote`                                                   | `credit_note.created`    |

**Transições inválidas (devem falhar alto):** `PAID`→qualquer coisa; `VOID`→qualquer coisa;
`UNCOLLECTIBLE`→`PAID` **sem** um pagamento reconciliado (com pagamento, é permitido e é um caso
real: cliente paga um boleto vencido). **[REC]** modele essa exceção como
`UNCOLLECTIBLE → PAID` explicitamente permitida somente pelo caminho de reconciliação, nunca por
ação de UI.

### 6.2 Subscription

**Estados (6):** `TRIALING` · `ACTIVE` · `PAST_DUE` · `PAUSED` · `CANCELED` · `INCOMPLETE`

Diferenças conscientes em relação ao spec (`OVERVIEW.md:37`) e ao briefing:

- **`INCOMPLETE` adicionado**: assinatura criada cujo *primeiro* pagamento nunca ocorreu. Não é
  o mesmo que `PAST_DUE` — aqui o serviço **nunca** foi concedido, então não há nada a revogar,
  e a expiração é silenciosa. Sem esse estado, a UI mente sobre entitlement no dia 1.
- **`EXPIRED` rejeitado**: é `CANCELED` com `cancel_reason`. Dois estados terminais que se
  comportam igual só fragmentam query e relatório.

| De → Para                                         | Gatilho                                   | Side effects                                              | Evento                   |
|---------------------------------------------------|-------------------------------------------|-----------------------------------------------------------|--------------------------|
| — → `TRIALING`                                    | criação com `trial_days>0`                | agenda fim do trial                                       | `subscription.created`   |
| — → `INCOMPLETE`                                  | criação com cobrança imediata pendente    | abre fatura + checkout                                    | `subscription.created`   |
| — → `ACTIVE`                                      | criação sem trial e sem cobrança pendente | primeiro período                                          | `subscription.created`   |
| `INCOMPLETE` → `ACTIVE`                           | primeira fatura paga                      | concede entitlement                                       | `subscription.activated` |
| `INCOMPLETE` → `CANCELED`                         | janela de ativação expirou                | —                                                         | `subscription.canceled`  |
| `TRIALING` → `ACTIVE`                             | fim do trial + fatura paga                | —                                                         | `subscription.activated` |
| `TRIALING` → `PAST_DUE`                           | fim do trial + fatura não paga            | dunning                                                   | `subscription.past_due`  |
| `ACTIVE` → `ACTIVE`                               | renovação                                 | novo período, nova fatura                                 | `subscription.renewed`   |
| `ACTIVE` → `PAST_DUE`                             | fatura do ciclo não paga no vencimento    | dunning, notifica                                         | `subscription.past_due`  |
| `PAST_DUE` → `ACTIVE`                             | fatura em aberto quitada                  | restaura entitlement                                      | `subscription.recovered` |
| `PAST_DUE` → `CANCELED`                           | política de dunning esgotada              | revoga entitlement, fatura → `UNCOLLECTIBLE`              | `subscription.canceled`  |
| `ACTIVE`/`TRIALING` → `PAUSED`                    | ação do merchant                          | suspende geração de fatura; **não** suspende usage record | `subscription.paused`    |
| `PAUSED` → `ACTIVE`                               | resume                                    | recalcula próximo período                                 | `subscription.resumed`   |
| `ACTIVE` → `CANCELED`                             | cancelamento imediato                     | pró-rata → `CreditNote`, revoga entitlement               | `subscription.canceled`  |
| `ACTIVE` → `ACTIVE` (`cancel_at_period_end=true`) | cancelamento agendado                     | nenhuma mudança de estado até o fim do período            | `subscription.updated`   |
| `CANCELED` → *(nenhum)*                           | terminal                                  | reativar = nova assinatura                                | —                        |

**Decisão explícita [REC]:** upgrade/downgrade **não** é transição de estado — é mudança de
`SubscriptionItem.price_id` + `Discount`/`CreditNote` de pró-rata, emitindo
`subscription.updated`. Tratar como estado é o erro que gera "estado combinatório".

### 6.3 CheckoutSession

**Estados:** `OPEN` · `COMPLETED` · `EXPIRED` · `CANCELED`

| De → Para            | Gatilho                                      | Side effects                                                                 | Evento                       |
|----------------------|----------------------------------------------|------------------------------------------------------------------------------|------------------------------|
| — → `OPEN`           | criação (via API ou botão "pagar" no portal) | gera URL, TTL (default **[REC]** 30 min para PIX), reserva idempotência      | `checkout.session.created`   |
| `OPEN` → `COMPLETED` | pagamento confirmado pelo wallet             | marca fatura `PAID` **na mesma transação lógica**, redireciona `success_url` | `checkout.session.completed` |
| `OPEN` → `EXPIRED`   | TTL vencido sem pagamento                    | libera reserva; fatura permanece `OPEN`                                      | `checkout.session.expired`   |
| `OPEN` → `CANCELED`  | usuário abandona / merchant cancela          | `cancel_url`                                                                 | `checkout.session.canceled`  |

**Inválidas:** qualquer coisa saindo de `COMPLETED`; reabrir `EXPIRED` (cria sessão nova).
**Invariante crítica:** a sessão **não** é a fonte de verdade do pagamento — a fatura é. Sessão
expirada nunca pode desfazer uma fatura já paga (corrida real quando o PIX cai no segundo 1799).

### 6.4 PaymentAttempt

**Estados:** `PENDING` · `SUCCEEDED` · `FAILED` · `ABANDONED`

| De → Para               | Gatilho                                           | Side effects                                                       | Evento              |
|-------------------------|---------------------------------------------------|--------------------------------------------------------------------|---------------------|
| — → `PENDING`           | billing chama o wallet                            | grava `wallet_charge_id`, `Idempotency-Key = invoice_id:attempt_n` | `payment.attempted` |
| `PENDING` → `SUCCEEDED` | webhook do wallet ou reconciliação                | fatura → `PAID`                                                    | `payment.succeeded` |
| `PENDING` → `FAILED`    | webhook de falha                                  | motivo, agenda retry conforme política                             | `payment.failed`    |
| `PENDING` → `ABANDONED` | reconciliação não encontra a cobrança após janela | **alarme** — é sinal de bug de integração, não de negócio          | `payment.abandoned` |

**Invariante:** `SUCCEEDED` é terminal e só é alcançável com `wallet_charge_id` confirmado na
origem. Nunca por ação de UI. "Marcar como pago manualmente" (recebimento fora do sistema) é
**outra coisa**: um `PaymentAttempt` de método `manual` com registro de quem marcou — precisa
existir, precisa de permissão própria, e nunca deve se disfarçar de pagamento automático.

### 6.5 WebhookDelivery

**Estados:** `PENDING` · `DELIVERING` · `SUCCEEDED` · `FAILED` · `EXHAUSTED`

| De → Para                  | Gatilho                                    | Side effects                                                                                                                    |
|----------------------------|--------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------|
| — → `PENDING`              | `Event` criado + endpoint inscrito no tipo | enfileira                                                                                                                       |
| `PENDING` → `DELIVERING`   | worker pega                                | lock, timeout **[REC]** 10s                                                                                                     |
| `DELIVERING` → `SUCCEEDED` | HTTP 2xx                                   | grava status, duração, resposta (truncada)                                                                                      |
| `DELIVERING` → `FAILED`    | não-2xx / timeout                          | backoff exponencial **[REC]** 1m, 5m, 30m, 2h, 6h, 12h (6 tentativas ≈ 20h)                                                     |
| `FAILED` → `PENDING`       | retry automático ou manual                 | tentativa++                                                                                                                     |
| `FAILED` → `EXHAUSTED`     | tentativas esgotadas                       | marca endpoint como degradado; **[REC]** desabilita automaticamente após N eventos consecutivos exauridos e notifica o merchant |
| `EXHAUSTED` → `PENDING`    | reenvio manual pela UI                     | tentativas resetam                                                                                                              |

**Ordenação:** **[REC]** não prometa ordem. Prometa `event.id` monotônico + `occurred_at`, e
documente que o consumidor deve ignorar evento mais antigo que o último processado por entidade.
Fila FIFO por endpoint é caro e — como `_analysis/GENERAL-REPORT.md:66` mostra que já aconteceu
no `ctech-dfe` — é o tipo de garantia que se documenta e não se implementa.

---

## 7. Information Architecture

### 7.1 Uma aplicação ou duas? (a pergunta central do briefing)

Opções e trade-offs reais:

| Opção                                                                                                                                                                                | Prós                                                                                       | Contras                                                                                                        | Veredito                                                    |
|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------|
| App único com troca de papel no header                                                                                                                                               | Um deploy, um design system                                                                | Consumidor herda densidade de operador; permissão vira condicional em toda tela; risco de vazar dado de tenant | **Não**                                                     |
| Dois repositórios/apps                                                                                                                                                               | Isolamento máximo                                                                          | Duplicação de auth, tokens, cliente HTTP — a 3ª linhagem outra vez                                             | **Não**                                                     |
| **Um app Next.js, dois shells de rota** (`/console/*` merchant, `/portal/*` consumidor), layouts, navegação e densidade independentes, design tokens e cliente de API compartilhados | Uma base de código, duas experiências deliberadamente distintas; um deploy; um `DESIGN.md` | Exige disciplina para não vazar componente de console pro portal                                               | **[REC] Sim**                                               |
| Portal embutido no app de cada produto (posição do `ARCHITECTURE.md:17-20`)                                                                                                          | Zero UI nova no MVP                                                                        | Cada produto reimplementa fatura/assinatura; sem portal, o consumidor não tem onde pagar                       | **Só como complemento**, via um painel embutível em Phase 3 |

**[REC]** Um app, dois shells. Workspaces/troca de organização só quando `Organization` for de
verdade multi-membro (Phase 2) — antes disso, um seletor de organização é UI para um problema que
não existe.

### 7.2 Navegação — Console (merchant/operador)

Crítica à hipótese do briefing: a sua estrutura mistura *objetos de negócio* com *ferramentas*, e
promove "Checkout" a item de topo sem que ele seja um lugar onde se trabalha diariamente.

```
Visão geral                    (não "Dashboard" — é a página de trabalho, não de vitrine)
Faturas                        ← entrada operacional #1
  · Todas / Em aberto / Vencidas / Falhas de cobrança   (views salvas, não subpáginas)
Assinaturas
Clientes
Catálogo
  · Produtos  (Preços vivem dentro do Produto, não como item de menu)
  · Cupons                     (Phase 3)
Cobrança                       (dunning, tentativas, fila de retry, conciliação)
Desenvolvedores
  · Webhooks · Eventos · Logs de API · Chaves (link para ctech-account)
Relatórios                     (Phase 4 — não abrir antes de existirem números confiáveis)
Configurações
```

Diferenças em relação à sua proposta, com motivo:

- **`Payments/Charges` não é seção de topo.** Pagamento sempre é visto no contexto de uma
  fatura. Uma lista global de cobranças é uma tela de investigação — ela vive em *Cobrança*.
- **`Prices` não é item de menu.** Preço sem produto é ininteligível.
- **`Checkout · Sessions` não é item de topo.** Sessão de checkout é um artefato de debug; vive
  em *Desenvolvedores* ou dentro da fatura. **Payment Links**, quando existirem, viram uma aba
  do Catálogo (é uma forma de vender, não de depurar).
- **`Discounts` vira `Cupons` dentro de Catálogo.** Desconto aplicado aparece na assinatura/fatura.
- **`Analytics` renomeado e adiado.** Um item de menu que abre uma tela com números errados é
  pior que a ausência dele.

### 7.3 Navegação — Portal (consumidor)

O consumidor tem exatamente três perguntas: *o que eu pago, quanto, e quando*. A navegação deve
caber nisso.

```
Início        (próxima cobrança + pendências + assinaturas ativas, em uma tela)
Faturas
Assinaturas
Configurações (dados de cobrança, e-mails, forma de pagamento — link para ctech-wallet)
```

**[REC]** Sem "Pagamentos" como item separado no MVP: pagamento é sempre um detalhe da fatura.
Uma aba de histórico de pagamentos separada da de faturas cria a pergunta "por que estes dois
números não batem?" — que só é respondível com um estorno em tela, coisa que não temos ainda.

---

## 8. Customers, PII e LGPD

**[FATO]** O spec proíbe PII (`OVERVIEW.md:35`, `ARCHITECTURE.md:119-120`); o briefing pede PII.

**[REC] Resolução:** billing armazena o **mínimo necessário para faturar e notificar**, e nada
além:

| Campo                                        | Guardar?        | Motivo                                                                                                                       |
|----------------------------------------------|-----------------|------------------------------------------------------------------------------------------------------------------------------|
| `id`, `organization_id`, `external_ref`      | Sim             | Identidade e ligação com o produto                                                                                           |
| `name`                                       | Sim             | Aparece na fatura                                                                                                            |
| `email`                                      | Sim             | Sem ele não existe notificação de vencimento                                                                                 |
| `tax_id` (CPF/CNPJ)                          | **Sim, mas**    | Obrigatório para NFS-e. **[REC]** guardar cifrado em repouso e nunca retornar completo em listagem (mascarar até no console) |
| `phone`                                      | Opcional        | Só se houver notificação por SMS/WhatsApp — não há no MVP. **Não guardar.**                                                  |
| `address`                                    | Condicional     | Necessário para NFS-e em alguns municípios. **[REC]** guardar só quando a organização emite NFS-e                            |
| `locale`, `timezone`, `currency`             | Sim             | Baratos, evitam bug de data e formatação de valor                                                                            |
| `metadata`                                   | Sim, com limite | Contrato em § 5.4. Teto de tamanho **e** aviso explícito de "não coloque PII aqui" — metadata é onde PII não declarada sempre acaba |
| Dados de cartão / chave PIX / conta bancária | **Nunca**       | Domínio do wallet/PSP                                                                                                        |

Além disso: *soft delete* de `Customer` que preserva as faturas (fatura emitida é documento, não
pode sumir porque alguém pediu exclusão) e anonimiza o cadastro; exportação por titular; e um
`AuditLog` que registra *acesso* a `tax_id`, não só escrita. **Os prazos de retenção estão
fechados em D12** — tabela por tipo de registro, TTL gravado na criação do item, com a ressalva
de que TTL do DynamoDB apaga em até ~48 h após o vencimento e não serve como garantia de prazo
duro. Retenção configurável *por organização* fica fora do MVP: um prazo por tipo de registro
resolve, e prazo configurável é mais superfície para configurar errado.

**O buraco de LGPD que `metadata` abre, dito com clareza:** o campo é livre e é propagado em todo
webhook (§ 5.4). Basta um integrador escrever `metadata["cpf_titular"]` para PII não declarada
sair do sistema por um canal que nenhuma política cobre. Mitigações **[REC]**, todas baratas se
feitas no dia 1 e caras depois: (a) aviso na UI e na doc, (b) exclusão de `Customer` anonimiza
também o `metadata` das entidades ligadas, (c) o mesmo TTL/retenção aplicado ao resto do registro.
Detecção automática de CPF em valor de `metadata` é tentadora e **não** recomendada: dá falso
positivo, quebra integração legítima e cria a ilusão de conformidade.

---

## 9. Créditos — a distinção que evita virar Wallet

Você pediu rigor aqui, então vou ser direto sobre onde cada conceito pertence:

| Conceito                                | Definição                                         | Domínio        | Por quê                                                                                                               |
|-----------------------------------------|---------------------------------------------------|----------------|-----------------------------------------------------------------------------------------------------------------------|
| **Crédito de fatura** (`CreditNote`)    | Abatimento vinculado a uma fatura emitida         | **Billing**    | É correção comercial de um documento comercial                                                                        |
| **Crédito promocional**                 | "R$50 off na próxima fatura"                      | **Billing**    | Nasce e morre dentro de faturas; nunca vira dinheiro                                                                  |
| **Usage credits / créditos de consumo** | "1.000 emissões incluídas"                        | **Billing**    | É *quantidade de entitlement*, não valor. **[REC]** modele como contador por período, com unidade — nunca em centavos |
| **Saldo pré-pago em dinheiro**          | Cliente deposita R$100, consome ao longo do tempo | **Wallet**     | É custódia de dinheiro de terceiro                                                                                    |
| **Stored value / carteira**             | Saldo transferível/sacável                        | **Wallet**     | Regulado                                                                                                              |
| **Reembolso em dinheiro**               | Devolver ao meio de pagamento                     | **Wallet/PSP** | Billing apenas *registra* que ocorreu, via `CreditNote`                                                               |

**Teste único para decidir [REC]:** *"o titular pode, por qualquer caminho, transformar isso em
dinheiro na conta dele?"* Sim → wallet. Não → pode ficar no billing.

**Consequência de produto:** a assinatura "baseada em créditos" que você mencionou é
implementável em billing **desde que** os créditos sejam contadores de consumo por período
(entitlement), não centavos. Se o cliente puder comprar um pacote de R$500 em créditos e pedir
o dinheiro de volta, isso é saldo pré-pago e pertence ao wallet — billing só o *consome* pedindo
débito.

---

## 10. Notificações

**[REC] Billing decide *quando* notificar e *o quê*; não envia e-mail.**

Razões: **[FATO]** `ctech-account` já tem infraestrutura de e-mail e SMS
(`ctech-account/api/internal/email/`, `internal/sms/`). Reimplementar SES, templates,
bounce/complaint handling, opt-out e reputação de domínio no billing é a definição de duplicação
cara. E `Notification` como entidade no billing rapidamente vira uma fila de mensagens de segunda
categoria.

**[REC] Arquitetura:** billing emite `Event`; um consumidor de notificação (no MVP: um handler
dentro do próprio billing, sem entidade própria; depois: um serviço de notificações) mapeia
evento → template → canal. O que billing **precisa** persistir é apenas `notification_sent_at`
por (evento, canal, destinatário) — para não notificar duas vezes e para mostrar na timeline.

Catálogo mínimo de notificações do MVP: fatura emitida, lembrete de vencimento (D-3),
pagamento confirmado, pagamento falhou, fatura vencida, assinatura cancelada. Os demais
(assinatura criada, renovada) são ruído no início — **[REC]** adicionar sob demanda, não por
simetria.

---

## 11. Analytics — o que é honesto medir

| Métrica                             | Faz sentido?                                              | Quando  |
|-------------------------------------|-----------------------------------------------------------|---------|
| Faturado no período (emitido)       | Sim                                                       | MVP     |
| Recebido no período                 | Sim                                                       | MVP     |
| Em aberto + aging (0-30/31-60/60+)  | Sim — é a métrica operacional que realmente move dinheiro | MVP     |
| Vencido                             | Sim                                                       | MVP     |
| Taxa de sucesso de cobrança         | Sim — detecta quebra de integração                        | MVP     |
| Nº de clientes / assinaturas ativas | Sim                                                       | MVP     |
| Ticket médio                        | Sim (trivial)                                             | Phase 2 |
| Novas assinaturas / cancelamentos   | Sim                                                       | Phase 2 |
| **MRR**                             | **Só com definição explícita**                            | Phase 4 |
| **ARR**                             | Só como MRR×12, rotulado como tal                         | Phase 4 |
| **Churn**                           | **Enganoso antes de ~6 ciclos de dados**                  | Phase 4 |
| LTV / CAC                           | **Não** — CAC não é dado do billing                       | —       |

**Sobre MRR [REC]:** com plano `METERED`, MRR não tem definição única (receita normalizada?
média móvel? só a parte fixa?). Publicar um número chamado "MRR" que mistura receita variável é
enganar o próprio time. Se for exibir: mostre **"MRR contratado (somente componentes fixos)"** e
**"receita variável (média 3 meses)"** como dois números separados, com a fórmula visível na UI.

**Visão do consumidor:** gasto no mês/ano, próximas cobranças, gasto por assinatura. Todos
derivam de faturas — nenhum exige entidade nova. **[REC]** não mostrar gráfico de gasto no portal
antes de 3 meses de histórico; sem dados, um gráfico é decoração.

---

## 12. Security, Multi-tenancy & Reliability

### 12.1 Isolamento

**[DECIDIDO — D7]** `pk = {organization_id}#{livemode}` em **toda** entidade — não como atributo
filtrável. Filtro esquecido é a forma nº 1 de vazamento entre tenants; chave de partição torna o
esquecimento impossível de executar, porque não existe query sem partição.

Corolário em DynamoDB: **nenhum `Scan` em caminho de leitura de dados de tenant.** `Scan` ignora
partição por definição. Se um acesso só se resolve com `Scan`, falta uma GSI — não é permissão
para varrer a tabela.

### 12.2 Organizações, membros, RBAC

**[FATO]** Nenhum modelo de organização existe em `ctech-account`. `ctech-dfe` tem um completo
(`repositories/roles.go:18-48`: OWNER/ADMIN/USER/VIEWER com permissões `ação.recurso`).

Opções:

| Opção                                                                           | Custo agora        | Consequência                                                                                                                              |
|---------------------------------------------------------------------------------|--------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| (a) Tenant = `product_key`, sem membros                                         | Quase zero         | Suficiente para o Produto A; portal do consumidor autentica por `user_id` do account                                                      |
| (b) Extrair organização/membro para `ctech-account` como conceito de plataforma | Alto (mexe em dfe) | **Correto no longo prazo** — é onde identidade mora                                                                                       |
| (c) Copiar o modelo do dfe para o billing                                       | Médio              | **Terceira linhagem.** `_analysis/cross-stack-duplication.md` documenta esse padrão já tendo custado uma divergência real (`UpsertAttrs`) |

**[DECIDIDO — D1] A opção (a) morre.** Um merchant externo é uma organização com pessoas, papéis
e convites; `product_key` não expressa isso, e a chave primária precisa carregar
`organization_id` desde o dia 1 (D7).

**[DECIDIDO — D4] Mas (b) não é pré-requisito de Phase 0.** Sem merchant externo com data,
construir `Membership`/`Invitation` em repo de produção é risco comprado adiantado. O MVP fica com
uma **`Organization` mínima local ao billing**: `id`, nome de exibição, `payout_status` (D3),
`livemode`, e **um** dono (`account.user_id`). Sem convite, sem papel configurável, sem CRUD
self-service. Provisionamento à mão. **Sem CNPJ nem qualquer cadastro de empresa** (D10):
cadastro fiscal é do `ctech-dfe`, e duplicá-lo aqui cria duas fontes de verdade sobre a mesma
empresa — a segunda desatualiza sempre.

**Gatilho que promove (b) para trabalho real** — o que vier primeiro: primeiro merchant externo
contratado, **ou** necessidade de organização compartilhada entre `ctech-dfe` e `ctech-billing`.

**[REC] Caminho de menor dano para (b) quando o gatilho disparar, em três passos:**

1. **`Organization` + `Membership` + `Invitation` nascem em `ctech-account`**, não no billing.
   Justificativa: identidade de pessoas já mora lá (usuários, sessões, MFA, consent), e um
   cliente que use `ctech-dfe` **e** `ctech-billing` não pode ter que criar a mesma empresa duas
   vezes. **[FATO]** o account já tem o padrão de escopos por serviço
   (`ctech-account/api/internal/scopes/`), então autorizar "membro X pode `billing:invoices:write`
   na org Y" é extensão do que existe, não invenção.
2. **Billing referencia `organization_id` do account e guarda só o que é dele** (configuração de
   faturamento, numeração, política de dunning). Zero cópia de cadastro de pessoas.
3. **`ctech-dfe` migra depois**, mantendo sua organização fiscal (CNPJ, certificados, config de
   NFS-e) como entidade filha chaveada pelo `organization_id` da plataforma. Migração de dados
   real, mas de um repo só, e sem prazo acoplado ao billing.

A opção (c) — copiar o modelo do dfe — continua proibida em qualquer cenário
(`_analysis/cross-stack-duplication.md`). A `Organization` mínima de D4 **não** é a opção (c):
ela não replica papéis, permissões nem convites; é um registro de tenant com um dono, desenhado
para ser substituído pela referência ao account, não para crescer até virar um segundo modelo
de RBAC. Se ela começar a ganhar papéis configuráveis antes do gatilho, virou (c) sem ninguém
decidir — esse é o sinal de alerta a vigiar em revisão de PR.

### 12.3 Autorização

**[REC]** Três eixos de credencial, todos emitidos pelo `ctech-account`:

1. **M2M por produto** (`billing:invoices:write` etc.) — serviço criando cobrança.
2. **Usuário do console** — operador merchant, scopes + papel na organização.
3. **Usuário do portal** — consumidor; só enxerga faturas cujo `customer.external_ref` casa com
   a identidade dele. **[REC]** essa checagem em um único middleware, testada com um teste que
   tenta acessar fatura de outro cliente e espera 404 (**não** 403 — 403 confirma existência).

### 12.4 Test mode / live mode

**[REC] Decida no Phase 0, implemente como partição de dados desde a primeira migration.**
Adicionar `livemode` depois exige reescrever toda chave e toda query. O custo hoje: um campo na
chave (`{org}#{mode}`) e credenciais separadas no account. O custo depois: uma migração de dados
com risco financeiro.

**[REC]** Regras: nenhum dado cruza modos, jamais; webhooks de teste vão só para endpoints de
teste; **nenhuma chamada real ao wallet em test mode** — um fake determinístico que permite
simular sucesso/falha/timeout (isso é DX de verdade, e é barato).

### 12.5 Idempotência

**[REC]** Middleware HTTP único (`OVERVIEW.md:184-186` já propõe, e está certo): chave +
hash do corpo + resposta armazenada por 24h; repetição com corpo diferente → 409. Aplicado a
100% dos POST/PUT/PATCH, sem exceção por conveniência.

### 12.6 Confiabilidade

- **Reconciliação obrigatória** com o wallet (`ARCHITECTURE.md:67-71`) — webhook nunca é o único
  sinal.
- **Scheduler idempotente por construção** — a chave de § 5.3 garante que rodar duas vezes no
  mesmo dia não duplica fatura. **[REC]** teste que executa o scheduler 3× e assere 1 fatura.
- **Alarmes** nos dois sinais que custam receita silenciosamente: falha do scheduler e queda na
  taxa de sucesso de cobrança (`ARCHITECTURE.md:111-113` já identificou; está correto).
- **Rate limiting por organização**, não por IP — **[FATO]** `ctech-go-common/ratelimit` existe.
  E **[FATO]** `_analysis/GENERAL-REPORT.md:70` registra que o rate limit do account *falha
  aberto* quando o Valkey cai; não repita esse comportamento em rota mutante de billing.

---

## 13. Missing Features — o que você não mencionou (classificado)

| Feature                                                           | Classificação              | Motivo                                                                                                                                                                           |
|-------------------------------------------------------------------|----------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Numeração sequencial de fatura por organização**                | **MVP**                    | Requisito contábil no BR; retroagir numeração é impossível. Barato agora, caro depois.                                                                                           |
| **Modo "recebido fora do sistema"** (marcar pago manualmente)     | **MVP**                    | Vai acontecer no dia 1 (TED, acerto). Sem isso, o operador mente no sistema ou fica com fatura eterna. Precisa de permissão + audit próprios.                                    |
| **Reconciliação billing ↔ wallet como tela**                      | **MVP**                    | Sem ela, divergência só aparece quando um cliente reclama.                                                                                                                       |
| **Timezone e "dia" definidos explicitamente**                     | **MVP**                    | Todo billing tem um bug de fuso. **[REC]** tudo em UTC no banco, `America/Sao_Paulo` para decidir "que dia é hoje" no scheduler, documentado.                                    |
| **Arredondamento e moeda em inteiros**                            | **MVP**                    | `OVERVIEW.md` já usa centavos — manter, e definir política de arredondamento de pró-rata em um lugar só.                                                                         |
| **Entitlement/serviço concedido**                                 | **MVP (mínimo)**           | `subscription.active` precisa virar uma resposta consultável — senão cada produto reimplementa "esse cliente pode usar?". Uma rota `GET /v1.0/entitlements?customer_ref=` resolve. |
| **Request ID / correlation ID ponta a ponta**                     | **MVP**                    | Suporte impossível sem isso. Custa uma linha de middleware.                                                                                                                      |
| **Exportação CSV de faturas/assinaturas**                         | **Post-MVP**               | Primeiro pedido de todo financeiro; não precisa estar no dia 1.                                                                                                                  |
| **Recibo (≠ NFS-e)**                                              | **Post-MVP**               | No BR o documento fiscal é a NFS-e (`ctech-dfe`). Recibo é cortesia.                                                                                                             |
| **Invoice PDF**                                                   | **Post-MVP**               | Já adiado no spec, corretamente.                                                                                                                                                 |
| **Payment links**                                                 | **Post-MVP**               | É `CheckoutSession` sem expiração — quase de graça depois do checkout.                                                                                                           |
| **Promotion codes**                                               | **Future**                 | Só com checkout self-serve.                                                                                                                                                      |
| **Metered billing avançado** (agregação max/last/unique, janelas) | **Future**                 | Soma simples cobre o dfe.                                                                                                                                                        |
| **Revenue recognition**                                           | **Não recomendado**        | Contabilidade, não billing.                                                                                                                                                      |
| **Disputes/chargebacks**                                          | **Não recomendado (aqui)** | Do PSP/wallet. Billing no máximo *reflete* o resultado.                                                                                                                          |
| **Multi-moeda + FX**                                              | **Não recomendado agora**  | Sem cliente internacional é complexidade pura. Manter só o campo `currency`.                                                                                                     |
| **Motor de impostos**                                             | **Não recomendado**        | É `ctech-dfe`.                                                                                                                                                                   |
| **Dunning por SMS/WhatsApp**                                      | **Future**                 | Custo por mensagem + opt-in regulado. E-mail primeiro.                                                                                                                           |

---

## 14. What Not To Build

Lista explícita, com o domínio dono de cada item:

1. **Saldo, custódia, extrato de dinheiro** → `ctech-wallet`. Se o billing tiver uma tabela com
   coluna "saldo do cliente", o projeto falhou.
2. **Métodos de pagamento / tokenização de cartão / chave PIX** → wallet/PSP. Billing referencia
   por id opaco.
3. **Ledger de dupla entrada** → wallet. Billing tem faturas, não lançamentos.
4. **Reembolso executando movimento financeiro** → wallet. Billing emite `CreditNote` e *pede*.
5. **Motor fiscal / NFS-e / cálculo de ISS** → `ctech-dfe`.
6. **API keys, OAuth clients, MFA, consent, gestão de sessão** → `ctech-account` (**[FATO]** já
   implementados, `ctech-account/api/ENDPOINTS.md:290-353`).
7. **Envio de e-mail** → account/notificações. Billing decide o quando, não o como.
8. **Checkout embutido (JS no site do merchant)** → não construir agora. Superfície de XSS,
   CSP e suporte que hospedado não tem. Hospedado primeiro; embutido só com demanda real.
9. **Merchant of Record** (modelo Paddle) → muda a natureza jurídica e fiscal da CTech.
10. **Marketplace público de planos** → já fora de escopo no spec (`OVERVIEW.md:159`); manter fora.
11. **Revenue recognition / relatórios contábeis** → ERP/contabilidade.
12. **Fila FIFO com ordenação garantida de webhooks** → alto custo, baixo valor; **[FATO]** o
    ecossistema já documentou uma garantia FIFO que não existia (`_analysis/GENERAL-REPORT.md:66`).
13. **Portal do consumidor como "console em modo leitura"** → é o anti-padrão que o próprio
    briefing identifica; construir experiência separada ou não construir.

### Onde eu discordo de você diretamente

- **"Payments/Charges" como seção de topo do console** — não é um lugar de trabalho; é contexto
  de fatura. Vira gaveta de tabela sem dono.
- **`Plan` como entidade** — é uma abstração pior que `Product + Price`, e o momento de trocar é
  agora, quando custa zero.
- **Estados `pending`, `overdue`, `expired`, `refunded`** — são consultas ou outra entidade
  disfarçadas de estado. Cada estado supérfluo multiplica caminho de teste e de UI.
- **`PromotionCode` no MVP** — sem checkout self-serve, é uma tela que ninguém usa.
- **"Analytics" no MVP** — MRR e churn calculados sobre 3 semanas de dados produzem decisão
  errada com aparência de rigor.
- **Bulk actions** — você já pediu para não adicionar por conveniência; concordo e vou além:
  a única bulk action que se justifica no MVP é reenvio de webhook. Cancelar assinatura em lote
  é uma ação destrutiva irreversível em massa — não no primeiro ano.

---

## 15. Screen Inventory

Formato: objetivo · usuário · informação · ações · estados · componentes. Fase entre colchetes.

### Console (merchant/operador)

**C1. Visão geral** `[Phase 2]`
Objetivo: responder "algo precisa de mim hoje?" · Operador · Recebido no mês, em aberto, vencido,
falhas de cobrança nas últimas 24h, próximas faturas a emitir · Ir para lista filtrada · loading /
vazio (primeira semana) / erro parcial por bloco · lista compacta + números com rótulo, **sem
grid de cards decorativos**.

**C2. Faturas — lista** `[Phase 2]`
Objetivo: encontrar e agir sobre faturas · Operador · Nº, cliente, status, total, vencimento,
tentativas · Filtro por status/período/cliente, busca por nº ou cliente, ordenação, paginação por
cursor, exportar `[Post-MVP]` · loading skeleton de tabela / vazio / erro / sem permissão ·
tabela densa, views salvas, badge de status com glifo (não só cor).

**C3. Fatura — detalhe** `[Phase 2]` — a tela mais importante do produto
Header: número, status, total, cliente, vencimento, ações primárias · Resumo: subtotal, descontos,
impostos, total, pago, restante · Linhas (com período de cada linha, marcando pró-rata) ·
Tentativas de pagamento (data, método, resultado, motivo da falha, `wallet_charge_id`) · Timeline
(eventos) · Metadata · Ações: finalizar, cobrar agora, marcar como pago, anular, emitir nota de
crédito, reenviar e-mail, copiar link de pagamento · estados: rascunho vs emitida mudam quais
ações existem; ações destrutivas (anular) com confirmação que exige digitar o número da fatura.

**C4. Assinaturas — lista** `[Phase 2]` · filtros por status, plano, próxima renovação.

**C5. Assinatura — detalhe** `[Phase 2]`
Header: cliente, status, produto/preço, próximo ciclo, valor recorrente · Itens · Faturas geradas ·
Uso do período (se metered), com barra de consumo · Timeline · Ações: pausar, retomar, cancelar
(imediato vs fim do período — **duas ações distintas, nunca um checkbox escondido em um modal**),
trocar preço com prévia de pró-rata **antes** de confirmar.

**C6. Clientes — lista** `[Phase 2]` · **C7. Cliente — detalhe** `[Phase 2]`: assinaturas,
faturas, total faturado/pago/em aberto, timeline, metadata; `tax_id` mascarado com ação
"revelar" auditada.

**C8. Catálogo — produtos** `[Phase 2]` · **C9. Produto — detalhe** `[Phase 2]`: preços (ativo/
arquivado), assinaturas por preço, ação "novo preço" (nunca "editar preço" — o preço é imutável, e
a UI deve ensinar isso).

**C10. Cobrança — fila** `[Phase 3]`: tentativas pendentes/falhas, próxima retentativa, ação de
tentar agora, política de dunning vigente.
**C11. Conciliação** `[Phase 3]`: divergências billing ↔ wallet, com ação de resolver.

**C12. Webhooks — endpoints** `[Phase 3]` · **C13. Endpoint — detalhe** `[Phase 3]`: eventos
assinados, saúde (% de sucesso 24h), últimas entregas com status HTTP, duração, payload,
resposta, e reenvio individual ou em lote. **C14. Eventos** `[Phase 3]`: log de eventos com
payload e endpoints que receberam. **C15. Logs de API** `[Phase 3]`: método, rota, status,
request id, latência, chave usada.

**C16. Cupons** `[Phase 3]` · **C17. Configurações** `[Phase 2]`: organização, dados de emissor,
política de dunning, numeração de fatura, e-mails, retenção.

### Portal (consumidor)

**P1. Início** `[Phase 3]`: próxima cobrança (valor + data + o quê), pendências em destaque,
assinaturas ativas. Uma tela, sem gráfico.
**P2. Faturas** `[Phase 3]`: lista simples com status legível ("Vence em 3 dias", não "OPEN").
**P3. Fatura — detalhe** `[Phase 3]`: o que é, quanto, quando, botão pagar, comprovante quando
pago. Sem timeline técnica.
**P4. Assinaturas** `[Phase 3]` · **P5. Assinatura — detalhe** `[Phase 3]`: o que inclui, quanto
custa, quando renova, como cancelar (sem esconder o cancelamento).
**P6. Configurações** `[Phase 3]`: dados de cobrança, preferências de e-mail.

### Checkout (público, sem sessão autenticada obrigatória)

**X1. Página de pagamento** `[Phase 3]`: o que está sendo pago, valor, método (PIX no MVP),
QR + copia-e-cola, contagem de expiração, estado de confirmação em tempo real, sucesso/erro/
expirado com caminho de saída claro. **[REC]** esta é a única tela do produto que deve ser
otimizada para conversão, não para densidade.

### `metadata` na UI (§ 5.4)

Padrão único em toda página de detalhe de entidade, não um componente por tela: bloco no fim do
painel lateral, tabela chave/valor densa de duas colunas, edição inline, sem card decorativo.
Vazio some — não ocupa espaço anunciando que está vazio. Nunca aparece no portal do consumidor
nem na fatura pública. Aviso curto de "não coloque dado pessoal aqui" no estado de edição, uma
linha, não banner.

### Estados obrigatórios em toda tela

Loading (skeleton com a forma do conteúdo, não spinner), vazio (com a ação que resolve), erro
(com request id copiável), falha parcial (bloco degrada, página não), sem permissão (diz o que
falta), não encontrado. Para consumidor: nunca expor código de erro cru nem nome de estado interno.

---

## 16. MVP Scope (revisado após D1–D12)

**Dentro:** `Organization` mínima como tenant real, com papéis (D4, § 12.2) · Customer com
PII mínima (§ 8) · **`metadata` chave/valor nas entidades principais (§ 5.4)** · Product + Price
(FIXED e METERED) · Subscription com trial, pró-rata,
cancelamento imediato/fim de período · Invoice com a máquina de estados de § 6.1, numeração
sequencial por organização, CreditNote · UsageRecord idempotente · scheduler idempotente com
calendário BR · **CheckoutSession + página de pagamento PIX hospedada (X1)** · cobrança via wallet
pelo contrato estendido de § 3.3, com débito de saldo como tentativa prévia · PaymentAttempt +
dunning configurável mínimo · reconciliação · audit log · webhooks de saída assinados ·
idempotência em todo POST · entitlement consultável · **test/live mode como partição de dados**
(§ 12.4) · console com telas C2–C9, C17 · **portal do consumidor P1–P3**.

Itens que entraram por causa das decisões, não por escopo inflado: checkout hospedado e portal
(sem eles, D2 não tem onde acontecer); `metadata` (D8 — barato agora, e sem ele o primeiro
integrador pede campo novo na semana 1); e o gate de `payout_status` (D3), que é código pequeno e
precisa existir antes de qualquer merchant, não depois.

Saiu do MVP por D4: `Membership`/`Invitation`/convite/CRUD de organização em `ctech-account`.
A organização do MVP tem um dono e nasce por provisionamento manual.

**Fora do MVP:** cupons, analytics/MRR, payment links, PDF, multi-moeda, API logs, SDK,
busca por `metadata` (§ 5.4), onboarding self-service de merchant (admissão manual no início — é o
controle de risco mais barato que existe), e **qualquer fluxo em que o dinheiro caia na conta do
merchant** enquanto `AsaasCustodyEnabled=false` — bloqueado no servidor por D3, não escondido na UI.

**Critério de "pronto"** — um teste ponta a ponta: assinar → fatura gerada na data certa
(feriado incluso) → cobrança → webhook → `PAID` → NFS-e emitida via dfe. É essencialmente o que
`PLAN.md:41-43` já define, e continua sendo o teste certo.

---

## 17. Roadmap

Revisado após D1–D12. A antiga "Phase 6 — plataforma multi-merchant" desapareceu como fase
separada: com A+B decidido, suas partes se distribuíram entre 0, 1 e 3. A **0b sai do caminho
crítico** por D4 (sem merchant externo com data) e vira fase tardia, opcional até existir gatilho.

| Fase                                                        | Itens                                                                                                                                                                                                         | Prioridade    | Depende de                       | Complexidade   | Risco                                                                                                           |
|-------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------|----------------------------------|----------------|-----------------------------------------------------------------------------------------------------------------|
| **0 — Fundações**                                           | ADR de datastore **registrando D7**; **`pk = {organization_id}#{livemode}`**; `Organization` mínima local (D4); esqueleto Go sobre `ctech-go-common`; CDK com constructs do `ctech-cdk`; CI; scopes `billing:*` no account; idempotência + request id | P0            | —                                | Média          | **Alto se pulado** — tudo aqui é caro de retroagir                                                              |
| **1 — Domínio puro**                                        | Product/Price/Subscription/Invoice/UsageRecord/CreditNote; `metadata` (§ 5.4); pró-rata; feriados; datas com **roll-forward (D5)**; máquinas de estado; audit                                                 | P0            | 0                                | Alta           | Alto (bug matemático silencioso) — mitigação: funções puras + testes de tabela                                  |
| **2 — API + Console**                                       | M2M + usuário; RBAC dentro da organização mínima; console C2–C9, C17; entitlements                                                                                                                           | P0            | 1                                | Alta           | Médio                                                                                                           |
| **3 — Cobrança PIX ponta a ponta**                          | **Contrato estendido no wallet (§ 3.3)**; CheckoutSession; página de pagamento X1; PaymentAttempt; dunning; reconciliação; webhooks de saída; portal P1–P3                                                    | P0            | 2 + **wallet**                   | Alta           | **Alto — dependência cross-repo.** Único item que não depende só de você; começar o desenho junto com a Phase 1 |
| **4 — Plataforma de desenvolvedor**                         | Eventos, delivery log, reenvio, API logs, test mode na UI, docs, SDK                                                                                                                                          | P1            | 3                                | Média          | Baixo                                                                                                           |
| **5 — Billing avançado**                                    | Cupons, base+overage, analytics/MRR, payment links, PDF, exportação                                                                                                                                           | P2            | 4                                | Média          | Baixo                                                                                                           |
| **6a — Organização na plataforma** *(outro repo)*           | `Organization`/`Membership`/`Invitation` em `ctech-account` + migração da organização mínima (§ 12.2)                                                                                                         | **Sob gatilho** | 1º merchant externo **ou** organização compartilhada com `ctech-dfe` | Alta           | **Alto — mexe em repo de produção.** Por D4, não se paga esse risco antes do gatilho                             |
| **6b — Merchant recebe de verdade**                         | Custódia Asaas ligada (destrava o gate de D3), split/tarifa, KYB, onboarding self-service                                                                                                                     | **Bloqueada** | **D9**: jurídico + KYC/subconta Asaas testado ponta a ponta; chave com Artur | **Muito alta** | **Muito alto**                                                                                                  |

Diferenças em relação ao `PLAN.md` atual: a Phase 3 de lá ("Wallet integration") está declarada
como bloqueada por um contrato genérico grande; aqui ela é desbloqueada por uma extensão pequena
de um fluxo já em produção. O audit log sai de "Phase 4" para Phase 1 — retroagir audit é
reconstruir história que não foi gravada. E a organização na plataforma, que na revisão anterior
era a fase 0b no caminho crítico, virou **6a sob gatilho** por D4: sem merchant externo com data,
construir convite/papel/CRUD de organização em repo de produção é risco comprado adiantado. O que
**não** se adia é a chave primária de Phase 0 — é ela que torna a 6a uma migração de propriedade,
e não uma reindexação.

---

## 18. Implementation Plan (ordem técnica)

1. ~~Decidir Produto A vs B~~ — **feito (D1: A+B).**
2. **ADRs** (`docs/adr/`) registrando D1–D12: datastore DynamoDB + GSI de período (D7); test/live
   como partição; modelo de tenant e organização mínima sem CNPJ (D4/D10); roll-forward (D5);
   gate de `payout_status` (D3) e sua pré-condição (D9); teto de cobrança (D11); retenção/TTL
   (D12). Escrever o ADR mesmo com a decisão tomada — o valor está no
   *porquê* e nos limites aceitos, que é o que a próxima pessoa vai querer reabrir.
3. **Extrair antes de construir**: confirmar que `ctech-go-common` cobre dynamo/cache/problem/
   lock/oauth2client para o billing; o que faltar, adicionar **lá**, não aqui.
4. **Esqueleto + CI + CDK** com constructs existentes.
5. **Domínio puro sem I/O** (pró-rata, feriados, ciclos, máquinas de estado) — 100% testado antes
   de tocar em banco.
6. **Persistência + audit** na mesma transação lógica das transições.
7. **API M2M + idempotência + scopes**.
8. **Scheduler** com teste de execução repetida.
9. **Console** — e é aqui que `/impeccable` entra (§ 19), não antes.
10. **Contrato estendido com o wallet** — em paralelo desde a fase 5, porque é a dependência
    externa.
11. **Webhooks de saída** reaproveitando o padrão de `ctech-wallet/api/internal/services/m2m_webhook.go`
    — **[REC]** extrair para `ctech-go-common` em vez de copiar.
12. **Checkout + portal**.

---

## 19. Sobre UX/UI e `/impeccable`

Seguindo a ordem que você mesmo definiu: análise (este documento) → IA e fluxos (§ 7, § 15) →
telas necessárias → **então** `/impeccable`. Ainda não a invoquei, deliberadamente: aplicar
refinamento visual a uma IA que ainda não foi aprovada seria polir uma estrutura que pode mudar.

Quando chegar a hora, os insumos já existem no ecossistema: **[FATO]** `ctech-account/ui/DESIGN.md`
e `ctech-dfe/ui/DESIGN.md` usam um formato de tokens versionado (paleta, tipografia, raios,
espaçamento) sobre Next 16 + React 19 + Tailwind 4 + shadcn + Geist. **[REC]** billing ganha
tokens próprios (é um produto distinto, não um tema do dfe), mas **o mesmo formato de arquivo e a
mesma stack** — e uma decisão explícita de que console e portal compartilham tokens e divergem em
densidade, escala tipográfica e peso de navegação.

---

## 20. As 13 perguntas do briefing, respondidas

1. **O que é o CTech Billing?** O sistema de registro de *o que é devido, por quem, por quê e
   quando* — e o orquestrador do ciclo comercial de uma cobrança (emitir, cobrar, insistir,
   corrigir, comunicar).
2. **O que ele não é?** Wallet, ledger, banco, adquirente, ERP, sistema fiscal, provedor de
   identidade e servidor de e-mail. Nenhum desses por acidente.
3. **Usuários?** Operador merchant (hoje: a própria CTech), consumidor final, e — o usuário mais
   importante no início — **outro serviço** (`ctech-dfe`) via M2M.
4. **Entidades principais?** Organization, Customer, Product, Price, Subscription,
   SubscriptionItem, Invoice, InvoiceItem, PaymentAttempt, CreditNote, UsageRecord, Event,
   WebhookEndpoint/Delivery, CheckoutSession, Coupon/Discount, AuditLog — mais `metadata`
   chave/valor como atributo transversal (§ 5.4), não como entidade.
5. **Ciclo de vida?** § 6, cinco máquinas explícitas, transição centralizada por entidade.
6. **Como Consumer e Merchant interagem?** Merchant opera (densidade, filtro, investigação);
   consumidor compreende e paga (clareza, três perguntas). Portais separados, código compartilhado.
7. **Arquitetura da UI?** Um app Next.js, dois shells de rota, tokens compartilhados, layouts
   independentes (§ 7.1).
8. **O que já existe?** No `ctech-billing`: 451 linhas de documento e nada mais. No ecossistema:
   auth, débito de saldo, cobrança PIX avulsa com webhook, módulo Go comum, constructs CDK,
   design system — tabela em § 2.4.
9. **O que falta?** Tudo do § 2.2 e § 3.2. O único bloqueio *externo* é o contrato de cobrança
   com o wallet (§ 3.3).
10. **O que repensar?** `Plan` → Product+Price; `billing_timing` com `FIXED_MONTHLY`; estados
    derivados; "sem UI"; "sem PII"; audit/dunning/webhooks como "sugestões".
11. **MVP?** § 16.
12. **Evolução?** § 17.
13. **Decisões que precisam ser tomadas agora?** Eram sete, todas caras de retroagir:
    produto A vs B · datastore · test/live como partição · `organization_id` na chave primária ·
    Product+Price imutável · onde mora organização/RBAC · escopo do contrato com o wallet.
    **As sete estão fechadas em § 0 (D1–D12).** A única que ficou parcialmente adiada é onde mora
    organização/RBAC, e por decisão explícita (D4): registro mínimo no billing agora, migração
    para o `ctech-account` quando existir merchant externo ou necessidade compartilhada com o
    `ctech-dfe`.

---

## 21. Perguntas ainda abertas (ordenadas por impacto)

**Nenhuma.** As doze decisões de § 0 (D1–D12) fecham as sete estruturais do § 20.13 e as quatro
que restavam desta seção. Phase 0 e Phase 1 estão desbloqueadas.

O que sobra não é pergunta em aberto, é **trabalho agendado com dono**:

| Item                                                        | Dono              | Quando                                  |
| ----------------------------------------------------------- | ----------------- | --------------------------------------- |
| Orientação jurídica + KYC/subconta Asaas testado (D9)       | Artur             | Antes da Phase 6b, não antes disso      |
| Desenho da extensão do wallet com teto de 100000 (D11)      | billing + wallet  | Começar junto com a Phase 1, entregar na 3 |
| Elevar teto para um cliente específico                      | negócio           | Só quando existir plano acima de R$ 1.000,00 |
| Reabrir prazos de D12                                       | —                 | Só se aparecer exigência legal de prazo duro (aí vira job de expurgo, não TTL) |

**As duas premissas que, se mudarem, reabrem decisão** — vale vigiar, não vale antecipar:

1. **Aparecer merchant externo com data.** Dispara a Phase 6a inteira (D4) e antecipa D9.
2. **Alguém pedir consulta ad-hoc sobre faturamento.** D7 aceitou servir só as métricas
   pré-declaradas do § 11. A saída é export para S3/Athena — **não** trocar o datastore.
