# inkan email

A key mail of your own, designed with [React Email](https://react.email) and
built into the worker.

```sh
pnpm install
pnpm dev
```

`pnpm dev` previews `emails/license.tsx` in the browser with a made up key.
Change the component until the mail is yours. The logo and the colors are
constants at the top, they are part of the design. The footer and the company
details come from the settings of the worker, the same as for the built-in
mail.

```sh
pnpm build
```

This renders the mail with placeholders where the key and the customer go and
writes it into the worker, as `../worker/src/template.ts`. Then build the
worker and deploy it.

```sh
cd ../worker
pnpm install
pnpm build
```

Copy the files from `worker/bundle` over the ones in the folder that
`inkan worker export` wrote, and deploy from there as before. The Worker tab
of the product then says that the worker sends a mail of its own. Keep the
component somewhere safe, it is the source.

Run `git checkout ../worker/src/template.ts` and build again to get the
built-in mail back.

## What the worker fills in

| Prop         | Placeholder   |                                      |
| ------------ | ------------- | ------------------------------------ |
| `licenseKey` | `{{key}}`     | The key                              |
| `name`       | `{{name}}`    | `INKAN_PRODUCT_NAME` or the prefix   |
| `plan`       | `{{plan}}`    | The plan of the license              |
| `email`      | `{{email}}`   | Who the license is for               |
| `expires`    | `{{expires}}` | End of the license, or Never         |
| `footer`     | `{{footer}}`  | `INKAN_MAIL_FOOTER`, may be empty    |
| `company`    | `{{company}}` | `INKAN_MAIL_COMPANY`, may be empty   |

The props are plain text, so show them and do not branch on them. During the
build they hold the placeholder, not a value, so a condition on a prop is
always true. The logo and accent settings shape only the built-in mail.

The `<title>` is the subject of the mail. The plain text part stays the one of
the worker.

## Dark mode and small screens

Tailwind classes are inlined, except `sm:` and `dark:` variants, which end up
as media queries in the head of the mail. The component uses `dark:` for a
dark palette and `sm:` for the padding on a phone. Apple Mail and Outlook
follow them, Gmail ignores them and darkens the mail its own way.

Only whole spacing steps come out as pixels. `leading-5.5` renders as rem,
which Outlook on Windows ignores, so write `leading-[22px]` instead.
