// Sends the key mail over the HTTP API of a mail service. Most serverless
// platforms cannot speak SMTP. For another service, add a sender here.

export type Mail = {
	from: string;
	// A bare address, an answer goes to the sender without one.
	replyTo?: string;
	to: string;
	subject: string;
	text: string;
	html: string;
};
export type Send = (mail: Mail) => Promise<void>;

export type SendEnv = {
	RESEND_API_KEY?: string;
	// Scaleway Transactional Email. Scaleway keeps SCW_ for itself on its functions.
	INKAN_SCW_SECRET_KEY?: string;
	INKAN_SCW_PROJECT_ID?: string;
	INKAN_SCW_REGION?: string;
};

/** The sender the settings ask for, none when no mail service is set. */
export function sender(env: SendEnv): Send | undefined {
	if (env.RESEND_API_KEY) return resend(env.RESEND_API_KEY);
	if (env.INKAN_SCW_SECRET_KEY && env.INKAN_SCW_PROJECT_ID) {
		return scaleway(env.INKAN_SCW_SECRET_KEY, env.INKAN_SCW_PROJECT_ID, env.INKAN_SCW_REGION || 'fr-par');
	}
	return undefined;
}

function resend(apiKey: string): Send {
	return (mail) =>
		post(
			'resend',
			'https://api.resend.com/emails',
			{ Authorization: 'Bearer ' + apiKey },
			{
				from: mail.from,
				reply_to: mail.replyTo,
				to: [mail.to],
				subject: mail.subject,
				text: mail.text,
				html: mail.html
			}
		);
}

function scaleway(secretKey: string, projectId: string, region: string): Send {
	const url = `https://api.scaleway.com/transactional-email/v1alpha1/regions/${region}/emails`;
	return (mail) => {
		// "Name <address>" or a bare address.
		const named = mail.from.match(/^\s*(.*?)\s*<([^<>]+)>\s*$/);
		const from = named ? { name: named[1], email: named[2] } : { email: mail.from.trim() };
		return post(
			'scaleway',
			url,
			{ 'X-Auth-Token': secretKey },
			{
				from,
				to: [{ email: mail.to }],
				additional_headers: mail.replyTo ? [{ key: 'Reply-To', value: mail.replyTo }] : undefined,
				subject: mail.subject,
				text: mail.text,
				html: mail.html,
				project_id: projectId
			}
		);
	};
}

async function post(
	service: string,
	url: string,
	headers: Record<string, string>,
	body: unknown
): Promise<void> {
	const response = await fetch(url, {
		method: 'POST',
		headers: { ...headers, 'Content-Type': 'application/json' },
		body: JSON.stringify(body)
	});
	if (!response.ok) {
		throw new Error(`${service}: ${response.status}: ${(await response.text()).slice(0, 200)}`);
	}
}
