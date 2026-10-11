# Parecer técnico — BYOK Inter (o merchant conecta a própria conta PJ do Banco Inter)

Data: 2026-10-11
Status: **análise / pesquisa — nenhuma linha de código foi escrita ou alterada**
Escopo: CTech Billing (Finanças + cobrança de faturas), com reuso de `ctech-wallet` e proposta para `ctech-go-common`.

Convenção de rotulagem (a mesma de `2026-08-15-product-architecture-assessment.md`, mais duas):

- **[FATO]**: está escrito numa fonte citada (link) ou no código (caminho de arquivo).
- **[INF]**: inferência minha a partir de fatos citados; precisa de confirmação antes de virar decisão.
- **[REC]**: recomendação minha, com justificativa.
- **[ABERTO]**: pergunta sem resposta nas fontes públicas; vai para o dono ou para o advogado.

**Sobre as fontes.** O portal `developers.inter.co` é um site Docusaurus renderizado no navegador, e o
texto das referências não aparece num `curl` comum. As referências, porém, são geradas a partir de arquivos
OpenAPI 3 **publicados pelo próprio Inter** no mesmo domínio. Este parecer cita esses arquivos como fonte
primária:

| API | OpenAPI publicado pelo Inter | Página renderizada |
|---|---|---|
| Token OAuth | [swagger-token-yaml.yaml](https://developers.inter.co/redocusaurus/swagger-token-yaml.yaml) | [references/token](https://developers.inter.co/references/token) |
| Pix (cob, cobv, lotecobv, loc, pix, devolução, webhook) | [swagger-api-pix-yaml.yaml](https://developers.inter.co/redocusaurus/swagger-api-pix-yaml.yaml) | [references/pix](https://developers.inter.co/references/pix) |
| Pix Automático (rec, solicrec, cobr, locrec, webhookrec, webhookcobr) | [swagger-api-pix-automatico-yaml.yaml](https://developers.inter.co/redocusaurus/swagger-api-pix-automatico-yaml.yaml) | [references/pix-automatico](https://developers.inter.co/references/pix-automatico) |
| Cobrança v3 (Boleto com Pix / "Bolepix") | [swagger-cobranca-bolepix-yaml.yaml](https://developers.inter.co/redocusaurus/swagger-cobranca-bolepix-yaml.yaml) | [references/cobranca-bolepix](https://developers.inter.co/references/cobranca-bolepix) |
| Banking (extrato, saldo, pagamentos, webhooks banking) | [swagger-banking-yaml.yaml](https://developers.inter.co/redocusaurus/swagger-banking-yaml.yaml) | [references/banking](https://developers.inter.co/references/banking) |

Os guias (`/docs/...`) e o changelog foram lidos do bundle do portal e do feed oficial
[`/changelog/feed.json`](https://developers.inter.co/changelog/feed.json). Fontes não oficiais estão marcadas
como tal. Todas as consultas foram feitas em 2026-10-11.

---

## 1. Resumo executivo

**Viabilidade: sim.** O Inter publica tudo o que o BYOK precisa: Pix com vencimento (`cobv`), Boleto com Pix
(Cobrança v3), Pix Automático em produção desde 08/07/2025, extrato enriquecido paginado e webhooks com
reenvio e histórico. A arquitetura esboçada no § 12 item 2 do spec de Finanças se sustenta no essencial. Três
pontos mudam:

1. **[FATO] Cada merchant precisa criar e aprovar a própria integração no Internet Banking do Inter.** Não há
   fluxo OAuth de autorização de terceiros nem programa de integrador para essas APIs no portal. O merchant
   preenche um formulário, a integração fica "Em validação" (análise do Inter) e só depois baixa o
   certificado, a chave e o client_id/secret, e os entrega à CTech
   ([como criar](https://developers.inter.co/docs/introducao/como-criar-uma-aplicacao),
   [changelog 12/03/2026](https://developers.inter.co/changelog/12/03/2026),
   [changelog 10/04/2026](https://developers.inter.co/changelog/10/04/2026)). Esse é o maior atrito do produto e
   está fora do nosso controle. Entre 24/12/2025 e 12/03/2026 o Inter **suspendeu a criação de novas integrações
   de Pix e Banking** (saldo e extrato continuaram liberados)
   ([changelog 24/12/2025](https://developers.inter.co/changelog/24/12/2025)).
2. **[FATO] O billing hoje não alcança o Inter.** A API do Inter só resolve para IPv4 (o DNS de
   `cdpj.partners.bancointer.com.br` não tem registro AAAA), e as instâncias do billing não têm IPv4 público
   (`terraform/billing/compute.tf:1,76-97`). O BYOK precisa de uma saída IPv4, como a Lambda `pix-gateway` da
   wallet ([spec](../../../ctech-wallet/docs/specs/2026-07-13-pix-gateway-lambda-design.md)).
3. **[FATO] O sufixo de webhook muda conforme o produto**: `/pix` para Pix (cob/cobv), `/rec` e `/cobr` para
   Pix Automático, `/pix-pagamento` e `/boleto-pagamento` para Banking, e **nenhum** sufixo para a Cobrança
   v3. **[INF]** Um único domínio da CTech consegue receber webhooks de todos os merchants, desde que o segredo
   por tenant vá **no caminho** da URL e não na query string.

**Ordem recomendada [REC]:**

| Fase | O que entra | Por quê |
|---|---|---|
| 1 | **Conexão** (cofre de credenciais, token por tenant, saída IPv4, renovação anual do certificado) **+ extrato/saldo** para o Finanças | Só leitura, escopo `extrato.read`. É a única capacidade que o Inter manteve liberada durante a suspensão de 12/2025. Valida toda a infraestrutura do BYOK sem mexer em dinheiro de ninguém. |
| 1 | **Pix com vencimento (`cobv`)** para faturas | Segue o padrão Bacen (a mesma API existe em outros PSPs, o que facilita trocar de banco depois). A criação é **síncrona**: o `pixCopiaECola` volta na resposta. Tem multa, juros, desconto e abatimento. É quase o mesmo cliente HTTP que a wallet já usa para `cob`. Tem devolução por API. |
| 2 | **Cobrança v3 (Boleto com Pix)** | Necessária para clientes PJ que pagam boleto. A API é proprietária do Inter e **assíncrona** (`EM_PROCESSAMENTO` / `FALHA_EMISSAO`). Exige endereço completo do pagador. Por baixo é um `cobv` gerado com a chave do merchant, então fica natural depois da fase 1. |
| 3 | **Pix Automático** para recorrências e assinaturas | É o produto certo para assinatura, mas traz uma máquina de estados nova (recorrência → autorização do pagador → uma cobrança por ciclo, enviada de 2 a 10 dias antes do vencimento → retentativas). O Inter só oferece a CNPJs com pelo menos 6 meses de atividade. Entra depois que fases 1 e 2 provarem a conexão. |

---

## 2. Integração e credenciais

### 2.1 Como o merchant cria a integração

- **[FATO]** Só pessoa jurídica com conta Inter Empresas usa as APIs. O caminho no Internet Banking PJ é
  menu "Integrar" → "Nova integração", depois nome e descrição, depois **permissões (escopos)**. Na primeira
  vez o Inter exige o aceite dos **termos de uso de cada API**. Em seguida vem um formulário em três partes:
  "Seus clientes", "Sua empresa" e "Seu negócio". A integração fica "**Em validação**" e, enquanto isso,
  **não é possível criar outra integração**. Na aprovação o Inter manda e-mail e a integração aparece como
  "Novo". O merchant clica em "Download chave e certificado", a tela mostra client_id e client_secret
  **uma única vez**, e minutos depois o status vira "Ativo"
  ([como criar uma integração](https://developers.inter.co/docs/introducao/como-criar-uma-aplicacao)).
- **[FATO]** Esse processo de análise foi introduzido em 2026 "seguindo critérios internos e regulatórios"
  ([changelog 10/04/2026](https://developers.inter.co/changelog/10/04/2026),
  [12/03/2026](https://developers.inter.co/changelog/12/03/2026)).
- **[FATO, fonte não oficial]** Uma plataforma terceira (Pluggy) documenta o mesmo fluxo e mostra o que
  o cliente entrega a ela: Client ID, Client Secret, `Inter_API_Chave.key` e `Inter_API_Certificado.crt`. Também
  menciona um código SMS de 6 dígitos na criação
  ([Pluggy: tutorial Banco Inter Empresas](https://v2.docs.pluggy.ai/docs/tutorial-banco-inter-empresas)).
  **[INF]** Isso indica que o mercado opera assim, com a plataforma guardando o certificado do cliente.
  Não é, porém, uma autorização do Inter para esse uso (ver § 8).

### 2.2 O que é emitido, e se o certificado é único por integração

- **[FATO]** Cada integração emite **um par certificado + chave privada (mTLS)** e **um client_id + client_secret**
  (OAuth2). Os dois são exigidos em toda chamada
  ([OAuth 2.0](https://developers.inter.co/docs/introducao/oauth-2.0),
  [diagrama](https://developers.inter.co/docs/introducao/diagrama-de-funcionamento)).
- **[FATO] O certificado é da integração.** Ele é baixado na tela da integração, renovado pela integração e
  deixa de valer quando aquela integração é cancelada ("o certificado, a chave privada e os escopos de acesso
  vinculados a essa integração não poderão mais ser utilizados")
  ([como renovar](https://developers.inter.co/docs/introducao/como-renovar-uma-integracao)). A suposição do dono
  está **confirmada**: um certificado por integração, portanto um por merchant.
- **[FATO] Validade e renovação:** o certificado vale **1 ano**. A renovação abre **90 dias antes** do vencimento e
  continua possível por até **90 dias depois** de expirar. Ela gera **novo `.crt` e novo `.key`** e mantém
  client_id, client_secret e escopos ([como renovar](https://developers.inter.co/docs/introducao/como-renovar-uma-integracao),
  [changelog 19/11/2024](https://developers.inter.co/changelog/19/11/2024),
  [24/12/2025](https://developers.inter.co/changelog/24/12/2025)). Cancelar é **irreversível**.
- **[INF]** A renovação é feita pelo merchant no Internet Banking, e a CTech não consegue renová-la sozinha.
  O produto precisa avisar com antecedência (D-90, D-30, D-7), aceitar o upload do par novo e trocar o
  certificado sem downtime. Sem isso, cada merchant para de faturar uma vez por ano.

### 2.3 Token OAuth2

- **[FATO]** `POST https://cdpj.partners.bancointer.com.br/oauth/v2/token` (sandbox:
  `https://cdpj-sandbox.partners.uatinter.co/oauth/v2/token`), corpo `application/x-www-form-urlencoded` com
  `client_id`, `client_secret`, `grant_type=client_credentials` e `scope`. A chamada é feita sobre mTLS com o
  certificado da integração. A resposta traz `access_token`, `token_type`, `expires_in` e `scope`.
  **O token vive 1 hora. O rate limit é de 5 chamadas por minuto em produção e no sandbox**
  ([swagger-token](https://developers.inter.co/redocusaurus/swagger-token-yaml.yaml),
  [references/token](https://developers.inter.co/references/token#tag/Token/operation/Token)).
- **[ABERTO]** A documentação não diz se esse limite de 5/min vale por integração, por conta ou por IP de
  origem. Com muitos merchants saindo pela mesma Lambda, isso importa. O desenho por tenant (§ 9) gera
  cerca de 1 emissão por tenant por hora, o que é seguro em qualquer leitura.

**Escopos por endpoint** (extraídos de cada operação dos OpenAPI acima):

| Produto | Escopos |
|---|---|
| Pix cob | `cob.read`, `cob.write` |
| Pix cobv | `cobv.read`, `cobv.write`; lote: `lotecobv.read`, `lotecobv.write` |
| Locations | `payloadlocation.read`, `payloadlocation.write` |
| Pix recebidos / devolução | `pix.read`, `pix.write` |
| Webhook Pix | `webhook.read`, `webhook.write` |
| Pix Automático | `rec.read/.write`, `solicrec.read/.write`, `cobr.read/.write`, `payloadlocationrec.read/.write`, `webhookrec.read/.write`, `webhookcobr.read/.write` |
| Cobrança v3 (Bolepix) | `boleto-cobranca.read`, `boleto-cobranca.write` (incluindo o webhook) |
| Banking extrato/saldo | `extrato.read` |
| Banking webhooks | `webhook-banking.read`, `webhook-banking.write` |
| Pagamentos (fora do escopo BYOK) | `pagamento-pix.*`, `pagamento-boleto.*`, `pagamento-darf.write`, `pagamento-lote.*` |

**[REC]** A CTech **não deve pedir escopos de pagamento** (`pagamento-*`). O BYOK só cobra e lê, e pedir menos
reduz o dano possível se uma credencial vazar. Isso ajuda tanto na análise do Inter quanto na conversa com o
advogado.

### 2.4 Rate limits (produção / sandbox, por minuto)

| Grupo | Produção | Sandbox |
|---|---|---|
| cob, cobv, lotecobv, loc, pix, devolução | 120 | 10 |
| webhook Pix (CRUD e consulta de callbacks) | 10 | 10 |
| reenvio de callbacks (`POST /webhook/callbacks/retry`) | 5 | — |
| Pix Automático (rec, solicrec, cobr, locrec) | 120 | 10 |
| webhookrec / webhookcobr | 10 | 10 |
| Cobrança v3: emitir, consultar, PDF, cancelar, sumário | 120 | 10 |
| Cobrança v3: editar e consultar edição | 10 | 10 |
| Cobrança v3: webhook CRUD | 5 | 5 |
| Banking: extrato, extrato completo, PDF, saldo | **10** | 10 |

Fonte: o campo `description` de cada operação nos OpenAPI listados no topo.

### 2.5 Várias contas: o header `x-conta-corrente`

- **[FATO]** Desde 08/03/2023 uma integração pode operar "uma ou mais contas correntes **do seu grupo**". O
  header `x-conta-corrente` (só dígitos, com DV, sem zeros à esquerda) é obrigatório apenas quando a integração
  tem mais de uma conta associada ([changelog 08/03/2023](https://developers.inter.co/changelog/08/03/2023)).
  O mesmo header é **enviado nos callbacks de webhook** desde 10/04/2023, "especialmente importante para [...]
  sistemas de gestão que operam aplicações de múltiplos clientes"
  ([changelog 10/04/2023](https://developers.inter.co/changelog/10/04/2023)). Os OpenAPI de Pix e de Cobrança v3
  declaram esse header nos callbacks.
- **[INF]** "Do seu grupo" quer dizer contas da mesma empresa ou grupo econômico. **Não existe** uma
  integração da CTech que opere contas de merchants diferentes. O changelog fala de sistemas que operam
  "aplicações de múltiplos clientes", isto é, uma aplicação por cliente, que é exatamente o BYOK.
- **[FATO]** Há uma inconsistência na documentação: o exemplo de extrato completo usa `x-inter-conta-corrente`,
  enquanto o parâmetro declarado é `x-conta-corrente`
  ([swagger-banking](https://developers.inter.co/redocusaurus/swagger-banking-yaml.yaml)). Validar no sandbox.

### 2.6 Sandbox

- **[FATO]** O sandbox é um ambiente separado. A conta é criada no portal (a mesma conta da comunidade) em
  [developers.inter.co/sandbox](https://developers.inter.co/sandbox). As integrações já vêm com todos os escopos.
  O **certificado dura 30 dias**, os dados são fictícios e existem endpoints próprios para simular pagamento
  (`POST /cob/pagar/{txid}`, `POST /cobv/pagar/{txid}`, `POST /sandbox/cob/pagamento`,
  `POST /cobrancas/{codigoSolicitacao}/pagar`, `POST /sandbox/cobr/pagamento`, `PATCH /sandbox/rec/{idRec}/status`)
  ([criar no sandbox](https://developers.inter.co/docs/sandbox/como-criar-uma-aplicacao),
  [testar](https://developers.inter.co/docs/sandbox/como-testar-aplicacao)).
- **[FATO]** As páginas de sandbox exibiam, no momento da consulta, o aviso "Ambiente Sandbox indisponível no
  momento" (mesmas fontes).
- **[INF]** O spec de Finanças diz que "uma conta BYOK em modo teste fala com o sandbox do Inter". Isso
  pressupõe que o merchant crie uma **segunda** integração, no sandbox, e a renove a cada 30 dias. É atrito
  demais para o merchant. **[REC]** O modo teste do BYOK deve usar **uma integração sandbox da própria CTech**,
  compartilhada entre os tenants: os dados são fictícios, então não há dinheiro de ninguém em jogo. O
  particionamento por `livemode` (ADR 0003) continua valendo.

---

## 3. Pix Cobrança imediata (`cob`) e com vencimento (`cobv`)

Base: `https://cdpj.partners.bancointer.com.br/pix/v2` (sandbox: `https://cdpj-sandbox.partners.uatinter.co/pix/v2`).
Fonte de tudo nesta seção: [swagger-api-pix](https://developers.inter.co/redocusaurus/swagger-api-pix-yaml.yaml).
O Inter segue a API Pix do Bacen ([bacen/pix-api](https://github.com/bacen/pix-api)).

### 3.1 Endpoints

| Operação | `cob` | `cobv` |
|---|---|---|
| Criar com txid | `PUT /cob/{txid}` | `PUT /cobv/{txid}` |
| Criar sem txid (o Inter gera) | `POST /cob` | — |
| Revisar / remover | `PATCH /cob/{txid}` | `PATCH /cobv/{txid}` |
| Consultar | `GET /cob/{txid}` (opcional `revisao`) | `GET /cobv/{txid}` |
| Listar | `GET /cob` (inicio, fim, cpf/cnpj, status, paginação) | `GET /cobv` |
| Lote | — | `PUT/PATCH/GET /lotecobv/{id}`, `GET /lotecobv/{id}/sumario`, `GET /lotecobv/{id}/situacao/{situacao}`, `GET /lotecobv` |
| Pix recebidos | `GET /pix/{e2eId}`, `GET /pix` | idem |
| Devolução | `PUT /pix/{e2eId}/devolucao/{id}`, `GET /pix/{e2eId}/devolucao/{id}` | idem |

### 3.2 Campos

**[FATO] `cob` (cobrança imediata):** `calendario.expiracao` (segundos, padrão **86400**),
`devedor` opcional (`cpf` **ou** `cnpj`, mais `nome`), `valor.original` (`\d{1,10}\.\d{2}`, maior que zero),
`valor.modalidadeAlteracao` (0/1), `chave` (obrigatória, **tem de pertencer ao recebedor**),
`solicitacaoPagador` (até 140), `infoAdicionais[]` (até 50 itens). O `txid` é criado pelo recebedor, segue
`[a-zA-Z0-9]{26,35}` e é único por CNPJ do recebedor.

**[FATO] `cobv` (com vencimento):** os campos obrigatórios são `calendario`, `devedor`, `valor` e `chave`.

- `calendario.dataDeVencimento` (`YYYY-MM-DD`; pode ser pago "até esse dia, inclusive"),
  `calendario.validadeAposVencimento` (dias corridos após o vencimento; o padrão na resposta é **30**).
- `devedor`: pessoa física (`nome` e `cpf` obrigatórios) ou jurídica (`nome` e `cnpj`), mais `email`,
  `logradouro`, `cidade`, `uf` e `cep` opcionais.
- `valor.original`, mais:
  - `valor.multa` {`modalidade` 1 = valor fixo, 2 = percentual; `valorPerc`};
  - `valor.juros` {`modalidade` 1 a 8: valor ou percentual ao dia, mês ou ano, em dias corridos ou úteis; `valorPerc`};
  - `valor.abatimento` {`modalidade` 1/2; `valorPerc`};
  - `valor.desconto` {`modalidade` 1 a 6: valor ou percentual até datas fixas (`descontoDataFixa[]`), ou por
    antecipação em dia corrido ou útil (`valorPerc`)}.
- A resposta traz `recebedor`, `loc`, `revisao`, `status` e **`pixCopiaECola`** (BR Code, até 512 caracteres).

### 3.3 Ciclo de vida, expiração e QR

- **[FATO]** Os status são `ATIVA`, `CONCLUIDA`, `REMOVIDA_PELO_USUARIO_RECEBEDOR` e `REMOVIDA_PELO_PSP`.
  Só cobrança `ATIVA` pode ser alterada. Para remover, usa-se `PATCH` com
  `status=REMOVIDA_PELO_USUARIO_RECEBEDOR`, sem outras mudanças na mesma requisição.
- **[FATO]** O QR "copia e cola" vem **na resposta síncrona** da criação (`pixCopiaECola`). A imagem PNG fica por
  conta de quem integra. A wallet já gera isso (`ctech-wallet/pix-gateway/internal/inter/qr.go`).
- **[FATO]** O mesmo `txid` pode receber mais de um Pix (dois pagadores escaneando o mesmo QR). A wallet trata
  isso devolvendo o excedente (`ctech-wallet/pix-gateway/internal/inter/client.go`, campo `Payments`).

### 3.4 Devolução

- **[FATO]** `PUT /pix/{e2eId}/devolucao/{id}` com `valor` (a soma das devoluções não pode passar do valor do Pix),
  `natureza` (`ORIGINAL` ou `RETIRADA`) e `descricao` (até 140). O `id` é do cliente, segue `[a-zA-Z0-9]{1,35}` e
  funciona como chave de idempotência. Os status são `EM_PROCESSAMENTO`, `DEVOLVIDO` e `NAO_REALIZADO`. A
  janela é de **90 dias** a partir da liquidação do Pix original. Escopo `pix.write`.
- **[FATO]** O `GET /pix/...` também enxerga os Pix recebidos por QR de Boleto com Pix gerado no IBPJ ("Pix
  Cobrança(Cob ou CobV) gerado pela API Pix ou IBPJ(Boleto com Pix)"). Então a devolução de um boleto pago
  via Pix passa pela mesma API.

---

## 4. Cobrança v3: Boleto com Pix ("Bolepix")

Base: `https://cdpj.partners.bancointer.com.br/cobranca/v3`. Fonte:
[swagger-cobranca-bolepix](https://developers.inter.co/redocusaurus/swagger-cobranca-bolepix-yaml.yaml).

- **[FATO] A API de boletos v2 foi descontinuada.** A página [Nossas APIs](https://developers.inter.co/docs/introducao/nossas-apis)
  diz "descontinuada em 04/05/2026". O [changelog 18/05/2026](https://developers.inter.co/changelog/18/05/2026)
  diz "18/05/2025", provavelmente um erro de digitação. O [changelog 05/12/2025](https://developers.inter.co/changelog/05/12/2025)
  anunciava 31/12/2025. **Só a v3 deve ser usada.**

### 4.1 Endpoints

| Operação | Endpoint |
|---|---|
| Emitir (assíncrono) | `POST /cobrancas` → `{codigoSolicitacao}` (UUID) |
| Consultar | `GET /cobrancas/{codigoSolicitacao}` |
| Listar | `GET /cobrancas` (dataInicial, dataFinal, filtrarDataPor, situacao, cpfCnpjPessoaPagadora, seuNumero, paginação, ordenação) |
| Sumário | `GET /cobrancas/sumario` |
| PDF | `GET /cobrancas/{codigoSolicitacao}/pdf` → `{pdf: base64}` |
| Editar vencimento/valor (assíncrono) | `PATCH /cobrancas/{codigoSolicitacao}` → `codigoEdicao`; `GET /cobrancas/edicao/{codigoEdicao}` |
| Cancelar | `POST /cobrancas/{codigoSolicitacao}/cancelar` {`motivoCancelamento` até 50} → 202 |
| Webhook | `PUT/GET/DELETE /cobrancas/webhook`; `GET /cobrancas/webhook/callbacks`; `POST /webhook/callbacks/retry` |

### 4.2 Emissão: campos e regras

- **[FATO]** Campos obrigatórios: `seuNumero` (até 15), `valorNominal` (**mínimo 2,50**, máximo 99.999.999,99),
  `dataVencimento`, `numDiasAgenda` (0 a 60 dias após o vencimento até o cancelamento automático) e `pagador`
  com `cpfCnpj`, `tipoPessoa`, `nome`, **`endereco`, `cidade`, `uf`, `cep`**. São opcionais `bairro`, `email`,
  `ddd`, `telefone`, `numero` e `complemento`.
- **[FATO]** Opcionais da cobrança: `desconto` {`codigo` `PERCENTUALDATAINFORMADA`|`VALORFIXODATAINFORMADA`,
  `taxa` ou `valor`, `quantidadeDias`}; `multa` {`codigo` `PERCENTUAL`|`VALORFIXO`, `taxa`|`valor`}; `mora`
  {`codigo` `TAXAMENSAL`|`VALORDIA`, `taxa`|`valor`}; `mensagem.linha1..5` (até 78 cada); `beneficiarioFinal`;
  `notaFiscal` (desde 15/01/2026); `formasRecebimento` (padrão `["BOLETO","PIX"]`, ou `SEM_FORMA_PAGAMENTO`).
  "Atualmente não é possível informar somente PIX". Para isso o Inter manda usar o `cobv`.
- **[FATO]** O QR Pix usa **a chave Pix do próprio merchant**, escolhida automaticamente. Se a conta não tiver
  chave, o boleto sai sem QR.
- **[FATO]** Cobrança com vencimento no mesmo dia só pode ser emitida **até 19h59**.
- **[FATO] Idempotência do Inter:** a combinação (`seuNumero`, `valorNominal`, `dataVencimento`, `cpfCnpj` do
  pagador) não pode se repetir dentro de **30 minutos**.
- **[FATO] A emissão é assíncrona.** A resposta traz só o `codigoSolicitacao`. Linha digitável, código de
  barras, `nossoNumero`, `txid` e `pixCopiaECola` chegam pelo webhook ou por consulta. Enquanto
  `EM_PROCESSAMENTO`, os objetos `boleto` e `pix` podem não vir, e falhas viram `FALHA_EMISSAO` com callback
  ([changelog 08/07/2024](https://developers.inter.co/changelog/08/07/2024),
  [05/12/2025](https://developers.inter.co/changelog/05/12/2025)). Edições levam "até 30 minutos" para refletir.

### 4.3 Ciclo de vida

- **[FATO]** As situações são `EM_PROCESSAMENTO`, `FALHA_EMISSAO`, `A_RECEBER`, `ATRASADO`, `RECEBIDO`,
  `MARCADO_RECEBIDO`, `CANCELADO`, `EXPIRADO` e `PROTESTO`. `origemRecebimento` vale `BOLETO` ou `PIX`. O
  webhook dispara para `RECEBIDO`, `A_RECEBER`, `MARCADO_RECEBIDO`, `CANCELADO`, `EXPIRADO` e `FALHA_EMISSAO`.

### 4.4 Tarifas

- **[FATO]** O portal de desenvolvedores **não documenta tarifas**. O blog do Inter afirma que boletos são
  "gratuitos" e que o Pix é "ilimitado e gratuito" ([blog: API de pagamento](https://blog.inter.co/api-de-pagamento/),
  [blog: boleto com Pix](https://blog.inter.co/boleto-com-pix/)). Isso é material de marketing, não contrato.
- **[ABERTO]** Pedir ao Inter (ou ao merchant) a tabela de tarifas vigente da conta PJ para emissão e
  liquidação de boleto via API. A tarifa é do merchant, não da CTech, mas pesa na decisão dele.

### 4.5 `cobv` vs Bolepix: comparação

| Critério | `cobv` (API Pix) | Cobrança v3 (Bolepix) |
|---|---|---|
| Padrão | Bacen; existe igual em outros PSPs | Proprietária do Inter |
| Criação | **Síncrona**; `pixCopiaECola` na resposta | **Assíncrona**; dados por webhook ou consulta; pode falhar depois (`FALHA_EMISSAO`) |
| Meio de pagamento | Só Pix | Boleto e Pix (QR com a chave do merchant) |
| Dados do pagador | Nome e CPF/CNPJ (endereço opcional) | Nome, CPF/CNPJ **e endereço completo** (obrigatório) |
| Valor mínimo | > 0 | 2,50 |
| Multa/juros/desconto | Sim (tabela Bacen, 8 modalidades de juros) | Sim (modelo simplificado) |
| Edição | `PATCH` imediato enquanto `ATIVA` | `PATCH` assíncrono (até 30 min) |
| Cancelamento | `PATCH status=REMOVIDA_PELO_USUARIO_RECEBEDOR` | `POST .../cancelar` |
| PDF | Não (a CTech gera; o billing já tem `invoicepdf`) | Sim (base64) |
| Webhook | Por **chave Pix**, URL + `/pix` | Um por conta, URL exata |
| Devolução | API de devolução | Se pago via Pix, pela API Pix; via boleto, não há devolução por API |
| Reuso da wallet | Alto: mesmo cliente, mesmos tipos de `cob` | Baixo: cliente novo |
| Reuso em outros bancos | Alto | Nenhum |

**[REC]** Usar `cobv` como padrão das faturas, com fatura em PDF gerada pelo próprio billing. Bolepix vira
opção por fatura ou por cliente quando o pagador precisar de boleto (PJ, setor público). Só a fase 2.

---

## 5. Pix Automático

Fonte: [swagger-api-pix-automatico](https://developers.inter.co/redocusaurus/swagger-api-pix-automatico-yaml.yaml),
[Nossas APIs](https://developers.inter.co/docs/introducao/nossas-apis), guias do Bacen
([Guia de implementação v1.3](https://www.bcb.gov.br/content/estabilidadefinanceira/pix/automatico/guia_pix_automatico.pdf),
[FAQ de participantes](https://www.bcb.gov.br/content/estabilidadefinanceira/pix/pix-automatico-FAQ-participantes.pdf)).

### 5.1 Status: o que está em produção e o que foi só anunciado

- **[FATO] Em produção no Inter desde 08/07/2025** ("API Pix Automático disponível em produção!"). O guia de
  jornadas saiu em 03/07/2025 ([changelog 08/07/2025](https://developers.inter.co/changelog/08/07/2025),
  [03/07/2025](https://developers.inter.co/changelog/03/07/2025)). Os endpoints listados abaixo estão todos na
  referência atual. **Não encontrei nada que esteja apenas anunciado.**
- **[FATO]** O produto Pix Automático do Bacen é regido pela Resolução BCB nº 402/2024
  ([texto, fonte não oficial](https://www.legisweb.com.br/legislacao/?id=462331)). O lançamento foi em
  16/06/2025 ([notícia, fonte não oficial](https://www.opovo.com.br/noticias/economia/2025/06/16/amp/pix-automatico-estreia-com-promessas-de-velocidade-e-seguranca-entenda.html)).
- **[FATO] Restrição do Inter:** "De acordo com normativas do BACEN, a API Pix Automático pode ser oferecida
  apenas a CNPJs com pelo menos 6 meses de atividade" ([Nossas APIs](https://developers.inter.co/docs/introducao/nossas-apis)).
  O FAQ do Bacen diz apenas "Pessoa jurídica cujo número de inscrição no CNPJ esteja ativo", e exige que os PSPs
  controlem fraude e má conduta (FAQ, item 1.4). O guia do Bacen inclui EI e MEI (§ 12). **Fato a registrar:** a
  regra dos 6 meses é do Inter, atribuída por ele ao Bacen.

### 5.2 Endpoints (base `/pix/v2`)

| Recurso | Endpoints | Escopo |
|---|---|---|
| Recorrência | `POST /rec`, `GET /rec`, `GET /rec/{idRec}` (com `txid` para as jornadas 3 e 4), `PATCH /rec/{idRec}` (revisar ou cancelar) | `rec.*` |
| Solicitação de confirmação (jornada 1) | `POST /solicrec`, `GET /solicrec/{idSolicRec}`, `PATCH /solicrec/{idSolicRec}` | `solicrec.*` |
| Cobrança recorrente | `POST /cobr`, `PUT /cobr/{txid}`, `GET /cobr`, `GET /cobr/{txid}`, `PATCH /cobr/{txid}` (cancelar), `POST /cobr/{txid}/retentativa/{data}` | `cobr.*` |
| Location da recorrência | `POST /locrec`, `GET /locrec`, `GET /locrec/{id}`, `DELETE /locrec/{id}/idRec` | `payloadlocationrec.*` |
| Webhooks | `PUT/GET/DELETE /webhookrec`, `PUT/GET/DELETE /webhookcobr` | `webhookrec.*`, `webhookcobr.*` |

**[FATO] Campos principais de `POST /rec`:** `vinculo` {`objeto` (até 35), `devedor` {nome, cpf|cnpj},
`contrato` (até 35)}; `calendario` {`dataInicial`, `dataFinal` opcional, `periodicidade`
`SEMANAL|MENSAL|TRIMESTRAL|SEMESTRAL|ANUAL`}; `valor` {`valorRec` (fixo) **ou** `valorMinimoRecebedor` (piso
para o teto que o pagador definir)}; `politicaRetentativa` `NAO_PERMITE|PERMITE_3R_7D` (até 3 retentativas em
dias diferentes dentro de 7 dias corridos); `loc`; `ativacao.dadosJornada`. Status da recorrência: `CRIADA`,
`APROVADA`, `REJEITADA`, `EXPIRADA`, `CANCELADA`.

**[FATO] `POST /cobr`:** `idRec`, `infoAdicional` (até 140), `calendario.dataDeVencimento`, `valor.original`,
`ajusteDiaUtil` (padrão `true`), `recebedor` {`conta`, `tipoConta`, `agencia`}, `devedor` {email, endereço}.
**Uma cobrança por ciclo.** O ciclo é ancorado na `dataInicial`; quando o dia não existe no mês, vale o dia
válido anterior. Status da cobrança: `CRIADA`, `ATIVA`, `CONCLUIDA`, `EXPIRADA`, `REJEITADA`, `CANCELADA`.
Tentativas (`tentativas[]`): tipo `AGND` (original), `NTAG` (retentativa pós-vencimento) ou `RIFL` (reenvio por
erro), status `SOLICITADA/AGENDADA/PAGA/CANCELADA/REJEITADA/EXPIRADA`, códigos de rejeição pacs/pain (`AM02`,
`AC05` etc.). O cancelamento via `PATCH` só vale antes da data da primeira tentativa de liquidação.

### 5.3 Jornadas

**[FATO]** Do guia embutido na referência do Inter:

| Jornada | Uso típico | Passos na API do Inter |
|---|---|---|
| 1 | Contrato presencial (academia, escola): o recebedor pede o aceite por push no app do pagador | `POST /rec` → `POST /solicrec` → (aprovação) → `POST /cobr` |
| 2 | Assinatura iniciada por QR (streaming) | `POST /locrec` → `POST /rec` com `loc` → `GET /rec/{idRec}` (QR) → (aprovação) → `POST /cobr` |
| 3 | Paga agora e adere para os próximos ciclos | `POST /locrec` → `POST/PUT /cob` → `POST /rec` com `loc` e `txid` → `GET /rec/{idRec}?txid=` → `POST /cobr` |
| 4 | Fatura com vencimento: paga ou agenda e depois pode aderir | `POST /locrec` → `PUT /cobv/{txid}` → `POST /rec` → `GET /rec/{idRec}?txid=` → `POST /cobr` |

### 5.4 Restrições do arranjo (Bacen)

- **[FATO]** O PSP pagador rejeita a instrução de pagamento que não chegar "entre dois dias e dez dias corridos
  antes da data prevista para a liquidação" (Guia Bacen, § sobre rejeições; FAQ, item 4.4). **O billing precisa
  agendar o `POST /cobr` de cada ciclo entre D-10 e D-2.** Também são rejeitadas a cobrança com valor acima do
  teto autorizado, com valor diferente do fixo autorizado, a segunda cobrança no mesmo ciclo, o recebedor
  diferente do autorizado e a cobrança sem autorização vigente.
- **[FATO]** O pagador pode cancelar a autorização e os agendamentos. O recebedor não cancela uma retentativa
  pós-vencimento (FAQ, item 4.14).

### 5.5 Como o merchant usaria para assinaturas

**[INF]** As assinaturas do billing (ADR 0018) passariam a ter "Pix Automático" como forma de pagamento:

- na adesão, o billing cria a `rec` (jornada 2 para checkout novo; jornada 4 sobre a primeira fatura `cobv`,
  que encaixa no fluxo "fatura com vencimento" que já existe);
- um job diário emite a `cobr` de cada ciclo em D-7 (dentro da janela de 2 a 10);
- os webhooks `rec` e `cobr` conciliam o status;
- `politicaRetentativa=PERMITE_3R_7D` cuida da inadimplência.

Isso substitui o lembrete de pagamento manual por débito autorizado.

---

## 6. Webhooks

### 6.1 Cadastro por produto

| Produto | Cadastro | URL chamada pelo Inter | Retentativas |
|---|---|---|---|
| Pix (cob/cobv) | `PUT /pix/v2/webhook/{chave}` {`webhookUrl`} — **um por chave Pix** | `{webhookUrl}/pix` | 4: 20, 30, 60, 120 min |
| Pix Automático, recorrências | `PUT /pix/v2/webhookrec` | `{webhookUrl}/rec` | 4: 20, 30, 60, 120 min |
| Pix Automático, cobranças | `PUT /pix/v2/webhookcobr` | `{webhookUrl}/cobr` | 4: 20, 30, 60, 120 min |
| Cobrança v3 | `PUT /cobranca/v3/cobrancas/webhook` — **um por conta/integração** | `{webhookUrl}` (sem sufixo) | 4: 20, 30, 60, 120 min |
| Banking (pagamentos feitos) | `PUT /banking/v2/webhooks/{pix-pagamento\|boleto-pagamento}` | `{webhookUrl}/pix-pagamento`, `{webhookUrl}/boleto-pagamento` | 4: 5, 10, 30, 60 min |

Fontes: as expressões `callbacks` e as descrições dos OpenAPI de
[Pix](https://developers.inter.co/redocusaurus/swagger-api-pix-yaml.yaml),
[Pix Automático](https://developers.inter.co/redocusaurus/swagger-api-pix-automatico-yaml.yaml),
[Cobrança v3](https://developers.inter.co/redocusaurus/swagger-cobranca-bolepix-yaml.yaml) e
[Banking](https://developers.inter.co/redocusaurus/swagger-banking-yaml.yaml). O sufixo `/pix` é do padrão
Bacen: callback `"{$request.body#/webhookUrl}/pix"` em [bacen/pix-api `openapi.yaml` v2.10.0](https://github.com/bacen/pix-api).
O servidor de exemplo oficial do Inter expõe exatamente `/pix`, `/cobranca` e `/banking`
([inter-co/pj-webhook-server-java](https://github.com/inter-co/pj-webhook-server-java)).

- **[FATO] Restrições de URL:** obrigatoriamente `https://` (padrão `^https://[^\s]*$`) e TLS 1.2 ou superior
  ([como configurar](https://developers.inter.co/docs/webhooks/como-config-webhooks)). Existe um validador de URL no
  portal ([validar URL](https://developers.inter.co/docs/webhooks/validar-url-do-webhook)).
- **[FATO] Reenvio e histórico:** `GET /webhook/callbacks` (Pix), `GET /cobrancas/webhook/callbacks` e
  `GET /webhooks/{tipo}/callbacks` retornam cada tentativa (`webhookUrl` no momento do disparo, `payload`,
  `numeroTentativa`, `httpStatus`, `mensagemErro`). O reenvio é `POST /webhook/callbacks/retry` (até 50 txids,
  5/min), disponível desde 14/04/2025. O histórico fica consultável por **1 mês**
  ([changelog 14/04/2025](https://developers.inter.co/changelog/14/04/2025),
  [19/06/2023](https://developers.inter.co/changelog/19/06/2023)).
- **[FATO] IPs de origem:** lista publicada (faixas Zscaler e IPs AWS), atualizada em 21/07/2025
  ([como configurar](https://developers.inter.co/docs/webhooks/como-config-webhooks)).

### 6.2 Payloads

- **[FATO] Pix:** `{"pix":[{endToEndId, txid, valor, componentesValor, chave, horario, infoPagador,
  devolucoes[], pagador{nome, cpfCnpj}}]}`. O `cpfCnpj` de pessoa física chega mascarado (ex.: `***853226**`).
  Também dispara quando uma devolução atinge status final (Bacen).
- **[FATO] Cobrança v3:** um **array** de `{codigoSolicitacao, seuNumero, situacao, dataHoraSituacao,
  valorTotalRecebido, origemRecebimento, nossoNumero, codigoBarras, linhaDigitavel, txid, pixCopiaECola}`.
- **[FATO] Pix Automático:** `{"recs":[{idRec, status, atualizacao[], encerramento, ativacao}]}` e
  `{"cobsr":[{idRec, txid, status, atualizacao[], tentativas[], pix[]}]}`.
- **[FATO]** Todos levam o header `x-conta-corrente`.
- **[FATO]** O Inter não documenta assinatura do corpo nem header de segredo. A autenticação documentada é o
  mTLS (abaixo).

### 6.3 mTLS nos callbacks

- **[FATO]** O receptor deve "validar a origem da chamada com a chave pública (ca.crt)", baixada no Internet
  Banking em "Minhas integrações" → "Certificado Webhook". Desde 06/03/2023 o download contém **somente** o
  `ca.crt`, descrito como "a chave pública do Inter" para "aceitar notificações apenas oriundas do Inter"
  ([como configurar](https://developers.inter.co/docs/webhooks/como-config-webhooks),
  [changelog 06/03/2023](https://developers.inter.co/changelog/06/03/2023)). O exemplo oficial em Node.js usa
  `requestCert: true` e `ca: ca.crt`, e rejeita a requisição quando `!req.client.authorized`
  ([criando o servidor](https://developers.inter.co/docs/webhooks/callback-webhook)). O exemplo em Java importa o
  `ca.crt` num truststore e liga `setNeedClientAuth(true)`
  ([pj-webhook-server-java](https://github.com/inter-co/pj-webhook-server-java)).
- **[INF]** O Inter, portanto, **apresenta um certificado de cliente** assinado pela CA contida no `ca.crt`. A
  documentação trata esse arquivo como "do Inter" e não como "da integração". Não há menção a um `ca.crt` por
  conta. É **provável** que seja o mesmo para todos os clientes, mas isso **não está escrito**.
  **[REC] Verificação barata:** comparar o `ca.crt` baixado pela conta Inter da CTech (o mesmo usado em
  `ctech-wallet`, bucket `{env}-ctech-wallet-pix-gateway-truststore`) com o de um segundo merchant piloto.
  Mesmo que sejam diferentes, o desenho continua de pé: o truststore do API Gateway aceita um *bundle* de CAs,
  e a separação entre tenants vem do segredo na URL, não da CA.

### 6.4 Um domínio para muitos merchants

**[INF] Viável, com estas regras:**

1. **Segredo no caminho, não na query.** O Inter acrescenta `/pix`, `/rec` e `/cobr` ao final da string
   cadastrada. Com `https://h/w/{token}`, as chamadas chegam em `/w/{token}/pix`, `/w/{token}/rec`,
   `/w/{token}/cobr`, e a Cobrança v3 chega em `/w/{token}`. Se o segredo estivesse na query
   (`...?hmac=X`), o sufixo poderia ser colado na própria query (`?hmac=X/pix`). A documentação não diz como o
   Inter compõe isso. `{token}` = id opaco do tenant + segredo aleatório de pelo menos 128 bits, comparado em
   tempo constante. O segredo pode ser trocado registrando de novo o webhook.
2. **mTLS na borda:** API Gateway HTTP API com domínio customizado **regional** e truststore = `ca.crt` do
   Inter. O registro DNS na Cloudflare fica **DNS-only**: se for proxied, a Cloudflare termina o TLS e o
   certificado do Inter não chega ao API Gateway. O endpoint `execute-api` padrão fica desativado. É o padrão
   já em produção na wallet (`ctech-wallet/cdk/lib/pix-gateway-stack.ts`).
3. **Conferência cruzada:** o `x-conta-corrente` do callback precisa bater com a conta registrada para o
   tenant; se não bater, o callback é descartado e gera alerta.
4. **Nunca confiar no payload:** o webhook só "acorda" o billing, que consulta de novo (`GET /cobv/{txid}`,
   `GET /cobrancas/{codigoSolicitacao}`, `GET /cobr/{txid}`) com as credenciais do tenant antes de dar a fatura
   como paga. É a Invariante 11 da wallet, já implementada lá.

**[INF] Conclusão:** um único host (ex.: `inter-hooks.billing.aoctech.app`) atende todos os merchants. O
fallback do esboço (wildcard `*.inter.billing.aoctech.app` com registros criados pela API da Cloudflare) **não é
necessário**.

### 6.5 Conflito com webhooks que o merchant já tem

- **[FATO]** O webhook Pix é **um por chave** (`PUT /webhook/{chave}`) e o da Cobrança v3 é **um por conta**
  (`PUT /cobrancas/webhook`, sem chave). `PUT` cria ou substitui.
- **[ABERTO]** Se o merchant já usa um ERP que cadastrou webhook na mesma chave ou conta, o cadastro da CTech
  **sobrescreve o do ERP**, ou o contrário. A documentação não diz se o webhook fica preso à integração ou à
  chave/conta. **[REC]** No onboarding, fazer `GET` antes de `PUT` e, se houver URL alheia, parar e perguntar ao
  merchant. Para Pix, recomendar uma chave EVP dedicada à CTech, já que o webhook é por chave.

### 6.6 Achado colateral, válido para a família: o webhook da wallet

**[FATO]** `ctech-wallet/OPERATIONS.md:128-131` manda cadastrar no Inter
`https://pix.wallet.aoctech.app/pix/webhook?hmac=<segredo>`, e a rota do API Gateway é exatamente
`POST /pix/webhook` (`cdk/lib/pix-gateway-stack.ts`). **[INF]** Pela especificação (§ 6.1), o Inter chama
`{webhookUrl}/pix`, o que pode cair em `/pix/webhook/pix` (rota inexistente) ou corromper o `?hmac=`. Nesse
caso os depósitos só seriam confirmados pelo *sweep* de reconciliação, e não pelo webhook.
**[REC]** Conferir em produção com `GET /pix/v2/webhook/callbacks` (que mostra a `webhookUrl` de cada disparo e o
`httpStatus`). Se confirmado, cadastrar a URL-base e mover o segredo para o caminho, como no § 6.4. Isso é
um ajuste na `ctech-wallet`, fora deste repositório.

---

## 7. Extrato e saldo (sincronização para o Finanças)

Fonte: [swagger-banking](https://developers.inter.co/redocusaurus/swagger-banking-yaml.yaml). Base `/banking/v2`, escopo `extrato.read`.

- **[FATO]** `GET /extrato?dataInicio&dataFim` (extrato simples: `dataEntrada`, `tipoTransacao`, `tipoOperacao`
  C/D, `valor`, `titulo`, `descricao`). **O período máximo por chamada é de 90 dias.**
- **[FATO]** `GET /extrato/completo` (enriquecido): `idTransacao`, `dataInclusao`, `dataTransacao`,
  `tipoTransacao`, `tipoOperacao`, `valor`, `titulo`, `descricao`, `numeroDocumento`, `detalhes`.
  - No Pix, `detalhes` traz `txId`, `endToEndId`, `nomePagador`, `cpfCnpjPagador`, `chavePixRecebedor`,
    `codigoSolicitacao` etc.
  - No boleto de cobrança, traz `nossoNumero`, `seuNumero`, `codBarras`, `juros`, `multa`, `desconto1..3`,
    `abatimento` etc.
  - Filtros: `tipoOperacao`, `tipoTransacao`, ambos opcionais.
  - Paginação tradicional (`pagina`, `tamanhoPagina`, até 10.000 transações) **ou** scroll (`scrollEnabled=true`,
    depois `scrollId`; até 10.000 por página; **um scroll ativo por conta**; expira após 6 minutos ociosos).
  - Período máximo de 90 dias. Até 05/09/2023 havia fallback para o extrato simples; hoje, se o enriquecido
    estiver indisponível, a chamada retorna erro
    ([changelog 05/09/2023](https://developers.inter.co/changelog/05/09/2023)).
- **[FATO]** `GET /saldo?dataSaldo` → `disponivel`, `bloqueadoCheque`, `bloqueadoJudicialmente`,
  `bloqueadoAdministrativo`, `limite`, `dataReferencia` (último dia útil quando a consulta cai em dia não útil).
  `GET /extrato/exportar` devolve um PDF em base64.
- **[FATO] Rate limit de 10 chamadas por minuto** para extrato, extrato completo, PDF e saldo.
- **[ABERTO] Profundidade do histórico:** a documentação limita a janela por chamada (90 dias), mas **não diz até
  quando no passado** é possível consultar. É preciso medir com um merchant piloto (ex.: pedir 24 meses em
  fatias de 90 dias).
- **[REC] Sincronização:**
  - incremental diária por `dataInclusao`, sobre uma janela móvel que relê os últimos 3 dias (lançamentos
    podem chegar com atraso);
  - deduplicação por `idTransacao`;
  - conciliação automática de fatura por `txId` / `codigoSolicitacao` / `seuNumero`;
  - backfill inicial em fatias de 90 dias, respeitando 10/min por conta;
  - fila por tenant, já que só pode haver um scroll ativo por conta.

  **[INF]** Isso cobre o "statement sync" do tier Custom no spec de Finanças.

---

## 8. Notas operacionais e jurídicas (só fatos; quem conclui é o advogado)

- **[FATO]** O acesso exige conta PJ Inter e aceite dos "termos de uso de cada API" no primeiro cadastro de
  escopos ([como criar](https://developers.inter.co/docs/introducao/como-criar-uma-aplicacao)). **Os termos não
  estão publicados no portal.** Só aparecem no Internet Banking.
- **[FATO]** O Inter alerta que client_id e client_secret "são credenciais privadas de acesso à sua conta e não
  devem ser compartilhadas em locais públicos", sob pena de cancelamento da integração (mesma fonte). O texto
  fala de locais públicos e não proíbe expressamente entregar as credenciais a um prestador de software.
- **[FATO]** O próprio Inter reconhece no changelog "sistemas de gestão que operam aplicações de múltiplos
  clientes" ([10/04/2023](https://developers.inter.co/changelog/10/04/2023)) e diz que as APIs podem ser
  integradas "até mesmo ao seu ERP" ([Nossas APIs](https://developers.inter.co/docs/introducao/nossas-apis)).
- **[FATO]** O portal **não tem** programa de "parceiro" ou "integrador" para as APIs bancárias. A única
  documentação de parceiros é de crédito imobiliário ([integração de parceiros](https://developers.inter.co/credito-imobiliario/api/integracao-parceiros-inter)).
  Isso foi verificado no [sitemap](https://developers.inter.co/sitemap.xml).
- **[FATO]** O formulário de criação pergunta pelo perfil dos **clientes** do titular e pelo modelo de negócio.
  O Inter faz análise "seguindo critérios internos e regulatórios" e já suspendeu novas integrações de Pix e
  Banking por quase 3 meses ([24/12/2025](https://developers.inter.co/changelog/24/12/2025) a
  [12/03/2026](https://developers.inter.co/changelog/12/03/2026)).
- **[FATO]** Lei 12.865/2013, art. 6º ([Planalto](https://www.planalto.gov.br/ccivil_03/_ato2011-2014/2013/lei/l12865.htm)):
  - I: arranjo de pagamento é o "conjunto de regras e procedimentos que disciplina a prestação de determinado
    serviço de pagamento ao público aceito por mais de um recebedor";
  - III: instituição de pagamento é quem, entre outras atividades, "b) executar ou facilitar a instrução de
    pagamento relacionada a determinado serviço de pagamento" e "c) gerir conta de pagamento".
- **[FATO]** No BYOK, a cobrança é criada **na conta do merchant**. No `cobv`, a `chave` "deve pertencer a este
  usuário recebedor". Na `cobr`, a conta `recebedor` tem de ser do usuário recebedor. Na Cobrança v3, o QR usa a
  chave do merchant. O dinheiro liquida na conta Inter do merchant e não passa por conta da CTech (OpenAPI de
  [Pix](https://developers.inter.co/redocusaurus/swagger-api-pix-yaml.yaml) e
  [Pix Automático](https://developers.inter.co/redocusaurus/swagger-api-pix-automatico-yaml.yaml)).
- **[ABERTO, para o advogado]**:
  1. Operar, com a credencial do merchant, a emissão de cobranças e o envio de instruções de pagamento
     recorrente (`cobr`) é "facilitar a instrução de pagamento" (art. 6º, III, b), mesmo sem tocar no dinheiro?
  2. Os termos de uso das APIs (aceitos pelo merchant no Internet Banking) permitem que um terceiro guarde e
     use o certificado e as credenciais? Peça ao merchant piloto uma cópia do texto aceito.
  3. A CTech precisa de algum contrato com o Inter, ou basta o contrato CTech ↔ merchant (mandato, LGPD:
     a CTech como operadora dos dados de pagadores que o merchant trata)?
  4. Isso tira o rail BYOK do alcance da questão do ADR 0005 (`docs/adr/0005-payout-gate.md`)? Na prática,
     o `payout_status` passaria a ter um caminho `enabled` sem custódia.

---

## 9. Confirmação e refutação do esboço (spec de Finanças, § 12 item 2)

Fonte do esboço: `docs/specs/2026-10-07-finance-erp-design.md:611-626`, mais a linha 47 (tabela de tiers) e a linha 92 (modo teste).

| # | Suposição do esboço | Veredito | Evidência |
|---|---|---|---|
| 1 | O certificado mTLS do cliente é usado **só na saída** | **Confirmado** | O par `.crt/.key` da integração autentica chamadas ao Inter. Na entrada se valida o `ca.crt` do Inter (§ 2.2, § 6.3) |
| 2 | `http.Transport` e cache de token por tenant | **Confirmado (necessário)** | Certificado por integração, token por client_id (60 min), emissão limitada a 5/min (§ 2.3) |
| 3 | Credenciais cifradas por espaço "seguindo o padrão de chave Asaas por usuário da wallet" | **Parcial** | Esse padrão foi **removido** da wallet (commit `23270a1`, "chore: remove Asaas BaaS integration"; era AES-256-GCM com chave-mestra única em `api/internal/asaas/crypto.go`). O equivalente atual no billing é o `crypto.Sealer` do ADR 0017. Faltava no esboço: o certificado **expira em 1 ano** e é trocado pelo merchant (§ 2.2) |
| 4 | "O Inter apresenta o mesmo certificado de cliente para todas as contas" | **Provável, não documentado** | O `ca.crt` é "a chave pública do Inter" e não há menção a um por conta (§ 6.3). A verificação é comparar dois downloads. O desenho não depende disso (bundle no truststore) |
| 5 | "O padrão Bacen acrescenta `/pix` à URL cadastrada" | **Confirmado, e incompleto** | `/pix` (Pix), `/rec` e `/cobr` (Pix Automático), `/pix-pagamento` e `/boleto-pagamento` (Banking). **Cobrança v3 sem sufixo** (§ 6.1) |
| 6 | Um domínio com a CA do Inter no truststore roteando `…/w/{opaque-id}/pix` com segredo por tenant | **Viável [INF]** | Segredo no caminho, não na query. Registro DNS-only. Conferência do `x-conta-corrente`. Nova consulta antes de baixar a fatura (§ 6.4) |
| 7 | Fallback com wildcard `*.inter.billing.aoctech.app` via API da Cloudflare | **Desnecessário [INF]** | O item 6 basta; o fallback só faria sentido se o Inter exigisse host por conta, e nada indica isso |
| 8 | `cobv` e boleto não existem em lugar nenhum da família | **Confirmado** | Nenhum código Go da família usa `/cobv`, `cobranca/v3` ou `/banking/v2/extrato`. A wallet só tem `cob`, `pix`, devolução e pagamento Pix (`pix-gateway/internal/inter/inter.go:53-60`) |
| 9 | Credenciais destruídas no disconnect e na exclusão do owner | **Correto, mas insuficiente** | Apagar do lado da CTech não revoga no Inter. Só o merchant cancela a integração (irreversível) no Internet Banking (§ 2.2). No disconnect a CTech deve antes fazer `DELETE` dos webhooks que cadastrou e depois orientar o merchant a cancelar a integração |
| 10 | O dinheiro cai no banco do cliente; a CTech nunca intermedeia | **Confirmado como fato técnico. Efeito jurídico em aberto** | § 8. A pergunta 1 ao advogado (art. 6º, III, b) é a que pode mudar o enquadramento |
| 11 | "Conta BYOK em modo teste fala com o sandbox do Inter" | **Parcial** | O sandbox exige outra integração, num portal separado, com certificado de 30 dias (§ 2.6). **[REC]** Usar uma integração sandbox da CTech, compartilhada, no modo teste |
| 12 | (implícita) O billing consegue chamar o Inter | **Refutado** | O Inter é só IPv4 (sem AAAA) e o billing não tem IPv4 de saída (`terraform/billing/compute.tf`). É preciso uma Lambda de saída (§ 10) |

---

## 10. Reuso da `ctech-wallet` e o que mover para a `ctech-go-common`

### 10.1 O que existe e se reusa

| Peça (wallet) | Onde | Reuso no BYOK |
|---|---|---|
| Cliente Inter mTLS + `do`/`doIdem`, `statusError`, `IsUnauthorized`, conversão reais ↔ centavos, saneamento do id de devolução `[a-zA-Z0-9]{1,35}` | `pix-gateway/internal/inter/inter.go`, `bearer.go`, `client.go` | **Alto.** Hoje usa um único tenant (config e SSM fixos). Precisa receber credenciais por chamada |
| `GetToken` (`/oauth/v2/token`, client_credentials) | `pix-gateway/internal/inter/inter.go` | **Alto.** Mesma chamada, só que por tenant |
| Gerente de token com single-flight (Valkey), renovação aos 55 min e retry único em 401 | spec `docs/specs/2026-07-13-inter-token-manager-design.md` (lado `api`) | **Alto.** O cache passa a ser chaveado por `tenant+livemode`, com lock por tenant via `ctech-go-common/lock` |
| Lambda de saída IPv4 (fora da VPC) chamada por `lambda:InvokeFunction` | `pix-gateway/cmd/outbound`, spec `2026-07-13-pix-gateway-lambda-design.md` | **Alto como padrão.** O billing precisa da sua própria (ou de uma compartilhada, ver 10.3) |
| Webhook: API Gateway HTTP API + domínio regional mTLS + truststore S3 + `disableExecuteApiEndpoint` + Lambda que só repassa txids para nova consulta | `cdk/lib/pix-gateway-stack.ts`, `pix-gateway/cmd/webhook/main.go` | **Alto como padrão.** Muda a autenticação (segredo no caminho, por tenant) e o roteamento por produto (`/pix`, `/rec`, `/cobr`, sem sufixo) |
| QR PNG a partir do copia e cola | `pix-gateway/internal/inter/qr.go` | Direto |
| Alertas SNS de falha nas Lambdas | `pix-gateway/internal/alerting` (wrapper da `ctech-go-common/alerts`) | Direto |
| Tratamento de vários Pix no mesmo txid, devolução do excedente, devolução idempotente | wallet `api` + `pix-gateway` | **Médio.** No BYOK, o excedente devolvido é dinheiro do merchant, o que é uma decisão de produto: devolver automaticamente ou marcar para o merchant decidir |

### 10.2 Para a `ctech-go-common` [REC]

Pela regra de família do `CLAUDE.md`, o cliente do Inter é código de provedor reutilizável, e não regra de
negócio. Ele deveria nascer em `ctech-go-common/inter` (hoje inexistente) e ser consumido pela wallet e pelo
billing:

- `inter.Credentials{ClientID, ClientSecret, CertPEM, KeyPEM, ContaCorrente, Env}` passadas **por chamada**
  (nada lido de SSM dentro do pacote);
- transporte mTLS cacheado por impressão digital do certificado, para que a troca anual crie um transporte novo;
- `TokenSource` com single-flight sobre `ctech-go-common/lock` + `cache`, chaveado por client_id;
- os clientes:
  - `pix` (cob, **cobv**, lotecobv, loc, pix, devolução, webhook, callbacks e retry);
  - `cobranca` (v3);
  - `pixauto` (rec, solicrec, cobr, locrec, webhooks);
  - `banking` (extrato, extrato completo com scroll, saldo);
- tipos de erro RFC 7807 do Bacen (`https://pix.bcb.gov.br/api/v2/error/<Tipo>`), conversão de dinheiro e
  validação de `txid` (`[a-zA-Z0-9]{26,35}`).

A wallet migra o `pix-gateway` para esse pacote num PR próprio. O contrato RPC dela não muda.

### 10.3 Uma Lambda de saída por serviço ou uma compartilhada

**[INF]** Uma Lambda "inter-egress" compartilhada entre wallet e billing economiza infraestrutura, mas mistura o
dinheiro custodiado da CTech (wallet, Invariante 12) com credenciais de terceiros. **[REC]** Manter uma Lambda
por serviço, cada uma com sua IAM role e seus segredos, e compartilhar só o **pacote** Go. Isso também mantém o
`api` do billing sem acesso direto à rede do Inter, como na wallet.

### 10.4 Onde guardar as credenciais do merchant

**[INF]** Na wallet, o certificado fica em parâmetros SSM fixos por ambiente (`/ctech-wallet/{env}/inter/mtls-*`).
Para N merchants isso não escala: cada SecureString padrão tem limite de 4 KB, então cert e chave ficam em
parâmetros separados, e haveria troca anual por merchant. **[REC]** Guardar uma linha por tenant no DynamoDB do
billing com o bundle selado pelo `crypto.Sealer` (ADR 0017) ou por envelope KMS. A Lambda de saída abre o
bundle uma vez por container e por tenant, e guarda em memória com TTL curto. Assim se evita KMS por chamada,
que é a mesma preocupação de custo do spec de token da wallet.

---

## 11. Riscos e perguntas em aberto

### Riscos

1. **Onboarding fora do nosso controle.** Análise do Inter por merchant, possibilidade de suspensão de novas
   integrações (já aconteceu) e o requisito de 6 meses de CNPJ para o Pix Automático. **Mitigação:** produto
   que funciona em degradação, com extrato primeiro, e um texto de onboarding que explica a espera.
2. **Expiração anual do certificado** derruba a cobrança do merchant se ele não renovar. **Mitigação:**
   alertas D-90, D-30 e D-7, monitor de `notAfter`, e a renovação aceita até 90 dias após o vencimento.
3. **Sobrescrever webhooks do ERP do merchant** (§ 6.5). **Mitigação:** `GET` antes de `PUT` e chave EVP dedicada.
4. **Vazamento de credenciais de terceiros:** a CTech passa a guardar acesso de cobrança de N empresas.
   **Mitigação:** só escopos de leitura e cobrança (nunca `pagamento-*`), selagem, segredos fora do `api`,
   trilha de auditoria por uso e rotação do segredo do webhook.
5. **Assincronia da Cobrança v3** (`EM_PROCESSAMENTO` → `FALHA_EMISSAO`): a fatura pode parecer emitida e não
   estar. **Mitigação:** estado intermediário explícito no billing, e `FALHA_EMISSAO` com alerta.
6. **Janela de 2 a 10 dias do Pix Automático:** um job atrasado faz o ciclo ser rejeitado pelo PSP pagador.
   **Mitigação:** emitir em D-7, com nova tentativa até D-3 e alarme.
7. **Rate limit de 10/min no extrato:** o backfill de contas grandes fica lento. **Mitigação:** fila por tenant.
8. **Webhook da wallet possivelmente sem uso** (§ 6.6): é um risco atual e fora deste repositório.

### Perguntas ao dono

1. O tier Custom exige **conta Inter**, ou o desenho deve prever outros PSPs desde já? O `cobv` é padrão Bacen e
   facilita os dois caminhos.
2. Na fase 1, a fatura BYOK é **só Pix (`cobv`)**? Boleto entra só na fase 2?
3. Pix pago a mais ou em duplicidade numa fatura BYOK: devolver automaticamente (como a wallet faz) ou deixar o
   merchant decidir?
4. O modo teste usa uma integração sandbox **da CTech** (recomendado) ou do merchant?
5. Quem é o merchant piloto (de preferência com CNPJ de mais de 6 meses e sem ERP já cadastrado em webhook)?

### Perguntas ao advogado

As quatro do § 8.

### A confirmar com o Inter ou com o piloto

1. Se o `ca.crt` do webhook é o mesmo entre contas.
2. Se o limite de 5/min do token vale por integração.
3. A profundidade do histórico do extrato.
4. A tabela de tarifas de boleto via API.
5. O texto dos termos de uso das APIs.
6. Se o webhook pertence à integração ou à chave/conta.
7. Como o Inter compõe o sufixo quando a URL tem query string.

---

## 12. Escopo sugerido para o primeiro spec

**Título sugerido:** "BYOK Inter — conexão, extrato e Pix com vencimento" (fase 1).

**Dentro:**

1. **Pacote `ctech-go-common/inter`**: credenciais por chamada, transporte mTLS, `TokenSource` por client_id,
   clientes `pix` (`cobv`, `pix`, devolução, webhook e callbacks) e `banking` (extrato completo com scroll,
   saldo). A migração da wallet para esse pacote fica para outro PR, fora deste spec.
2. **Lambda de saída do billing** (IPv4, fora da VPC), com contrato RPC mínimo: `Token`, `CreateCobV`,
   `GetCobV`, `PatchCobV`, `ListPix`, `Refund`, `PutWebhook`/`GetWebhook`/`DeleteWebhook`, `Statement`, `Balance`.
3. **Conexão do merchant no console/Finanças:**
   - upload de `.crt`, `.key`, client_id e client_secret, mais a chave Pix e a conta;
   - um "teste de conexão" que pede o token, consulta o saldo e lê o `notAfter` do certificado;
   - selagem por tenant;
   - lista de escopos exigidos com instruções passo a passo;
   - renovação: upload do par novo sem downtime e alertas de vencimento;
   - disconnect: `DELETE` dos webhooks, apagar o bundle e orientar o merchant a cancelar a integração.
4. **Webhook multi-tenant:** API Gateway mTLS (`ca.crt` do Inter), `…/w/{token}/pix`, conferência do
   `x-conta-corrente`, nova consulta antes de baixar a fatura, e `GET /webhook/callbacks` + retry como
   reconciliação diária.
5. **Fatura paga por `cobv`:** emissão (`PUT /cobv/{txid}` com multa, juros e desconto a partir das regras da
   fatura), QR e copia e cola no portal do pagador, baixa por webhook ou sweep, devolução.
6. **Sincronização de extrato** para o Finanças: backfill em fatias de 90 dias, incremental diário e
   conciliação automática por `txId`.
7. **Modo teste** com a integração sandbox da CTech; nenhuma linha atravessa os modos (ADR 0003).
8. **Gate:** o `payout_status` ganha o caminho "BYOK conectado", condicionado ao parecer jurídico (D9 do ADR 0005).

**Fora (specs seguintes):**

- Cobrança v3 (Boleto com Pix), na fase 2;
- Pix Automático para assinaturas, na fase 3;
- lote `lotecobv`;
- qualquer escopo de pagamento (`pagamento-*`);
- outros PSPs.

**Pré-requisitos antes de escrever o plano:**

1. Resposta do advogado às perguntas do § 8.
2. Merchant piloto com integração aprovada no Inter.
3. Comparação do `ca.crt` entre duas contas.
4. Conferência do sufixo `/pix` no webhook da wallet (§ 6.6).
